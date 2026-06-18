package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
)

const (
	telegramAPIBase               = "https://api.telegram.org/bot"
	telegramFileBase              = "https://api.telegram.org/file/bot"
	telegramMaxAudioBytes         = 20 << 20 // Telegram bot download cap (~20MB)
	telegramMaxImageBytes         = 20 << 20 // Telegram bot download cap (~20MB)
	telegramChunkChars            = 3900
	telegramBusyMessage           = "Still processing previous request. Please wait for my reply first."
	telegramAckReaction           = "👍"
	telegramDefaultPollTimeoutSec = 30
	telegramDefaultStatusInterval = 2 * time.Second
	telegramPollRetryBase         = 1 * time.Second
	telegramPollRetryMax          = 30 * time.Second
	// Telegram caps long-poll well under a minute; bound config to sane ranges.
	telegramMaxPollTimeoutSec    = 50
	telegramMaxStatusIntervalSec = 300
	// Bound shutdown so an unresponsive ACP child cannot wedge the gateway.
	telegramShutdownTimeout = 5 * time.Second
)

// telegramTokenPattern matches the documented Telegram bot token shape
// (<numeric bot id>:<secret>) and rejects whitespace or other characters that
// would be unsafe to splice into a request URL path.
var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

type telegramClient interface {
	Call(ctx context.Context, method string, params any, out any) error
}

func gatewayTelegramCmd(cfgPath *string) *cobra.Command {
	var once bool

	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the Telegram gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			if once {
				return runTelegramGatewayOnce(cmd.Context(), cfgPath)
			}
			return runTelegramGateway(cmd.Context(), cfgPath)
		},
	}

	cmd.Flags().BoolVar(&once, "once", false, "run one polling cycle and exit")
	return cmd
}

type telegramRuntimeConfig struct {
	PollTimeout     int
	RunDir          string
	Workspace       string
	StreamResponses bool
	StatusInterval  time.Duration
	AllowedChatIDs  map[int64]struct{}
	Provider        string
}

type telegramGatewaySession struct {
	chatID     int64
	sessionID  string
	inFlight   bool
	output     strings.Builder
	lastStatus time.Time
	mu         sync.Mutex
}

type telegramImageAttachment struct {
	MIMEType string
	Data     []byte
	Path     string
}

type telegramGateway struct {
	ctx        context.Context
	http       *http.Client
	token      string
	cfg        telegramRuntimeConfig
	cli        telegramClient
	pollOffset int64

	telegramCallFn func(method string, params any, out any) error

	sessionsByChat  map[int64]*telegramGatewaySession
	sessionsByAcpID map[string]*telegramGatewaySession
	sessionsMu      sync.RWMutex

	// acpClosed is closed exactly once when the ACP transport drops, so the
	// poll loop can exit instead of prompting against a dead client.
	acpClosed     chan struct{}
	acpClosedOnce sync.Once
}

func (g *telegramGateway) markACPClosed() {
	g.acpClosedOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "telegram gateway: ACP connection closed; shutting down")
		close(g.acpClosed)
	})
}

func runTelegramGateway(ctx context.Context, cfgPath *string) error {
	gw, stop, err := setupTelegramGateway(ctx, cfgPath)
	if err != nil {
		return err
	}
	defer func() { _ = stop() }()

	backoff := telegramPollRetryBase
	for {
		select {
		case <-gw.acpClosed:
			return fmt.Errorf("telegram gateway: ACP connection closed")
		default:
		}

		if err := gw.pollOnce(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "telegram gateway poll error: %v\n", redactTelegramTokenError(err, gw.token))
			select {
			case <-ctx.Done():
				return nil
			case <-gw.acpClosed:
				return fmt.Errorf("telegram gateway: ACP connection closed")
			case <-time.After(backoff):
			}
			if backoff < telegramPollRetryMax {
				backoff *= 2
				if backoff > telegramPollRetryMax {
					backoff = telegramPollRetryMax
				}
			}
			continue
		}

		backoff = telegramPollRetryBase
	}
}

func runTelegramGatewayOnce(ctx context.Context, cfgPath *string) error {
	gw, stop, err := setupTelegramGateway(ctx, cfgPath)
	if err != nil {
		return err
	}
	defer func() { _ = stop() }()
	return gw.pollOnce()
}

func setupTelegramGateway(ctx context.Context, cfgPath *string) (*telegramGateway, func() error, error) {
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return nil, nil, err
	}

	normalizedCfg := normalizeTelegramConfig(cfg.Telegram)

	token := strings.TrimSpace(cfg.Telegram.BotToken)
	if token == "" && strings.TrimSpace(cfg.Telegram.BotTokenEnv) != "" {
		token = strings.TrimSpace(os.Getenv(cfg.Telegram.BotTokenEnv))
	}
	if !cfg.Telegram.Enabled || token == "" {
		return nil, nil, fmt.Errorf("telegram gateway is disabled or telegram token is missing")
	}
	if !telegramTokenPattern.MatchString(token) {
		return nil, nil, fmt.Errorf("telegram bot token is malformed; expected <bot_id>:<secret>")
	}

	gw := &telegramGateway{
		ctx:             ctx,
		http:            &http.Client{Timeout: telegramHTTPClientTimeout(normalizedCfg.PollTimeout)},
		token:           token,
		cfg:             normalizedCfg,
		sessionsByChat:  make(map[int64]*telegramGatewaySession),
		sessionsByAcpID: make(map[string]*telegramGatewaySession),
		telegramCallFn:  nil,
		acpClosed:       make(chan struct{}),
	}

	cli, stopServer, err := runACPServer(ctx, *cfgPath, normalizedCfg.Provider, func(note acp.Notification) {
		_ = gw.handleACPNotification(note)
	})
	if err != nil {
		return nil, nil, err
	}
	gw.cli = cli
	gw.telegramCallFn = gw.telegramCall

	return gw, func() error {
		_ = gw.closeAllSessions()
		return stopServer()
	}, nil
}

func normalizeTelegramConfig(cfg config.TelegramConfig) telegramRuntimeConfig {
	pollTimeout := cfg.PollTimeoutSec
	if pollTimeout <= 0 {
		pollTimeout = telegramDefaultPollTimeoutSec
	}
	if pollTimeout > telegramMaxPollTimeoutSec {
		pollTimeout = telegramMaxPollTimeoutSec
	}
	statusIntervalSec := cfg.StatusIntervalSec
	if statusIntervalSec > telegramMaxStatusIntervalSec {
		statusIntervalSec = telegramMaxStatusIntervalSec
	}
	statusInterval := time.Duration(statusIntervalSec) * time.Second
	if statusInterval <= 0 {
		statusInterval = telegramDefaultStatusInterval
	}

	allowedChatIDs := make(map[int64]struct{}, len(cfg.AllowedChatIDs))
	for _, chatID := range cfg.AllowedChatIDs {
		if chatID != 0 {
			allowedChatIDs[chatID] = struct{}{}
		}
	}

	return telegramRuntimeConfig{
		PollTimeout:     pollTimeout,
		RunDir:          strings.TrimSpace(cfg.RunDir),
		Workspace:       strings.TrimSpace(cfg.Workspace),
		StreamResponses: cfg.StreamResponses,
		StatusInterval:  statusInterval,
		AllowedChatIDs:  allowedChatIDs,
		Provider:        strings.TrimSpace(cfg.Provider),
	}
}

func telegramHTTPClientTimeout(pollTimeoutSeconds int) time.Duration {
	if pollTimeoutSeconds <= 0 {
		return time.Duration(telegramDefaultPollTimeoutSec+10) * time.Second
	}
	return time.Duration(pollTimeoutSeconds+10) * time.Second
}

func runACPServer(ctx context.Context, cfgPath, provider string, onNotification func(acp.Notification)) (*acp.Client, func() error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve executable: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)

	args := []string{"acp"}
	if strings.TrimSpace(cfgPath) != "" {
		args = append(args, "--config", cfgPath)
	}
	if strings.TrimSpace(provider) != "" {
		args = append(args, "--provider", provider)
	}

	child := exec.CommandContext(ctx, exe, args...)
	child.Stderr = os.Stderr

	stdin, err := child.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("acp stdin: %w", err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("acp stdout: %w", err)
	}

	if err := child.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, fmt.Errorf("start acp process: %w", err)
	}

	client := acp.NewClient(stdout, stdin, onNotification)
	client.StartLaunch()

	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := initializeACP(initCtx, client); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		return nil, nil, fmt.Errorf("initialize acp server: %w", err)
	}

	stop := func() error {
		if ctx.Err() == nil {
			finalizeCtx, cancel := context.WithTimeout(context.Background(), telegramShutdownTimeout)
			_ = client.Call(finalizeCtx, acp.MethodFinalize, acp.FinalizeParams{Reason: "gateway_exit"}, nil)
			cancel()
		}
		_ = child.Process.Kill()
		return child.Wait()
	}

	return client, stop, nil
}

func initializeACP(ctx context.Context, cli *acp.Client) error {
	var initRes acp.InitializeResult
	return cli.Call(ctx, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo: acp.ClientInfo{
			Name:    "v100-gateway",
			Version: "dev",
		},
		ClientCapabilities: acp.ClientCapabilities{},
	}, &initRes)
}

func (g *telegramGateway) pollOnce() error {
	// pollOnce is intentionally single-flight: updates are handled sequentially.
	// Concurrency per chat is not supported yet.
	updates, err := g.fetchUpdates()
	if err != nil {
		return err
	}

	for _, update := range updates {
		if update.UpdateID < g.pollOffset {
			continue
		}
		g.pollOffset = update.UpdateID + 1

		if update.Message == nil {
			continue
		}
		chat := update.Message.Chat
		// Gate on the allowlist before any expensive work (downloads, STT).
		if !g.chatAllowed(chat.ID) {
			continue
		}

		text := strings.TrimSpace(update.Message.textContent())
		var images []telegramImageAttachment
		if photo := update.Message.photoFile(); photo != nil {
			data, mimeType, perr := g.downloadImage(photo.FileID)
			if perr != nil {
				if g.ctx.Err() != nil {
					return nil
				}
				_ = g.sendChunks(chat.ID, []string{"Couldn't download your image."})
				fmt.Fprintf(os.Stderr, "telegram gateway image download error: %v\n", redactTelegramTokenError(perr, g.token))
				continue
			}
			img := telegramImageAttachment{MIMEType: mimeType, Data: data}
			path, werr := g.saveTelegramImage(chat.ID, update.Message.MessageID, len(images), img)
			if werr != nil {
				if g.ctx.Err() != nil {
					return nil
				}
				_ = g.sendChunks(chat.ID, []string{"Couldn't save your image for tool use."})
				fmt.Fprintf(os.Stderr, "telegram gateway image save error: %v\n", werr)
				continue
			}
			img.Path = path
			images = append(images, img)
			if text == "" {
				text = "User sent an image."
			}
		}
		if text == "" {
			if audio := update.Message.audioFile(); audio != nil {
				transcript, terr := g.transcribeVoice(chat.ID, audio)
				if terr != nil {
					if g.ctx.Err() != nil {
						return nil
					}
					fmt.Fprintf(os.Stderr, "telegram gateway transcription error: %v\n", redactTelegramTokenError(terr, g.token))
					continue
				}
				text = strings.TrimSpace(transcript)
			}
		}
		if text == "" {
			continue
		}
		if err := g.reactToMessage(chat.ID, update.Message.MessageID); err != nil {
			fmt.Fprintf(os.Stderr, "telegram gateway reaction error: %v\n", redactTelegramTokenError(err, g.token))
		}
		if err := g.handleTelegramMessage(chat.ID, text, images); err != nil {
			if g.ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "telegram gateway message error: %v\n", redactTelegramTokenError(err, g.token))
			continue
		}
	}

	return nil
}

func (g *telegramGateway) fetchUpdates() ([]telegramUpdate, error) {
	params := map[string]any{
		"timeout":         g.cfg.PollTimeout,
		"allowed_updates": []string{"message"},
	}
	if g.pollOffset > 0 {
		params["offset"] = g.pollOffset
	}

	var updates []telegramUpdate
	if err := g.telegramCall("getUpdates", params, &updates); err != nil {
		return nil, err
	}

	return updates, nil
}

func (g *telegramGateway) handleACPNotification(note acp.Notification) error {
	if note.Method == acp.MethodConnectionClosed {
		g.markACPClosed()
		return nil
	}
	if note.Method != acp.MethodSessionUpdate {
		return nil
	}

	var update acp.SessionUpdateParams
	if err := json.Unmarshal(note.Params, &update); err != nil {
		fmt.Fprintf(os.Stderr, "telegram gateway: dropping malformed %s payload: %v\n", note.Method, err)
		return nil
	}

	if strings.TrimSpace(update.SessionID) == "" {
		return nil
	}

	g.sessionsMu.RLock()
	state := g.sessionsByAcpID[update.SessionID]
	g.sessionsMu.RUnlock()
	if state == nil {
		return nil
	}

	switch update.Update.Type {
	case "agent_message_chunk":
		if update.Update.Content == nil || strings.TrimSpace(update.Update.Content.Text) == "" {
			return nil
		}
		// Streaming sends from the ACP reader loop; the gateway is currently
		// single-flight, so a slow Telegram API only delays this in-flight turn.
		if g.cfg.StreamResponses {
			return g.sendChunks(state.chatID, splitText(update.Update.Content.Text))
		}
		state.mu.Lock()
		state.output.WriteString(update.Update.Content.Text)
		state.mu.Unlock()
	case "run_status_update":
		if update.Update.Status != "in_progress" {
			return nil
		}
		state.mu.Lock()
		since := time.Since(state.lastStatus)
		state.mu.Unlock()
		if since < g.cfg.StatusInterval {
			return nil
		}
		if err := g.sendChatAction(state.chatID); err == nil {
			state.mu.Lock()
			state.lastStatus = time.Now()
			state.mu.Unlock()
		}
	case "run_error":
		if strings.TrimSpace(update.Update.Status) == "failed" {
			return g.sendChunkToChat(state.chatID, "Run failed. Check the run log for details.")
		}
	}

	return nil
}

// chatAllowed reports whether the gateway should act on messages from chatID.
// An empty allowlist allows all chats.
func (g *telegramGateway) chatAllowed(chatID int64) bool {
	if len(g.cfg.AllowedChatIDs) == 0 {
		return true
	}
	_, ok := g.cfg.AllowedChatIDs[chatID]
	return ok
}

func (g *telegramGateway) handleTelegramMessage(chatID int64, text string, images []telegramImageAttachment) error {
	if !g.chatAllowed(chatID) {
		return nil
	}

	state, err := g.getOrCreateSession(chatID)
	if err != nil {
		return err
	}

	state.mu.Lock()
	if state.inFlight {
		state.mu.Unlock()
		return g.sendChunks(chatID, []string{telegramBusyMessage})
	}
	state.inFlight = true
	state.output.Reset()
	state.mu.Unlock()

	defer func() {
		state.mu.Lock()
		state.inFlight = false
		state.mu.Unlock()
	}()

	promptText := text
	if len(images) > 0 {
		var b strings.Builder
		b.WriteString(text)
		b.WriteString("\n\nTelegram image attachments were saved as local files for tool use:")
		for i, img := range images {
			if strings.TrimSpace(img.Path) == "" {
				continue
			}
			fmt.Fprintf(&b, "\n- image %d: %s", i+1, img.Path)
		}
		b.WriteString("\nIf the user asks to post an attached image to Bluesky, do not ask for a path. Use atproto_upload_blob with the saved image_path above, then pass the returned cid, mime, size, and alt to atproto_post images[].")
		promptText = b.String()
	}

	prompt := []acp.ContentBlock{{Type: "text", Text: promptText}}
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(img.MIMEType)
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		prompt = append(prompt, acp.ContentBlock{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(img.Data),
			MimeType: mimeType,
		})
	}
	var promptRes acp.SessionPromptResult
	if err := g.cli.Call(g.ctx, acp.MethodSessionPrompt, acp.SessionPromptParams{
		SessionID: state.sessionID,
		Prompt:    prompt,
	}, &promptRes); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return g.sendChunks(chatID, []string{fmt.Sprintf("v100 error: %v", err)})
	}

	if g.cfg.StreamResponses {
		if promptRes.StopReason != "" && promptRes.StopReason != "end_turn" {
			return g.sendChunks(chatID, []string{fmt.Sprintf("Stopped: %s", promptRes.StopReason)})
		}
		return nil
	}

	state.mu.Lock()
	response := strings.TrimSpace(state.output.String())
	state.output.Reset()
	state.mu.Unlock()
	if response == "" {
		response = "(no response)"
	}
	return g.sendChunks(chatID, splitText(response))
}

func (g *telegramGateway) getOrCreateSession(chatID int64) (*telegramGatewaySession, error) {
	// pollOnce handles updates sequentially today. If per-chat concurrency is
	// added, this lookup/create path needs singleflight protection.
	g.sessionsMu.Lock()
	existing := g.sessionsByChat[chatID]
	g.sessionsMu.Unlock()
	if existing != nil {
		return existing, nil
	}

	sessionID := fmt.Sprintf("tg-%d", chatID)
	params := acp.SessionNewParams{
		SessionID: sessionID,
		CWD:       g.cfg.Workspace,
		RunDir:    g.cfg.RunDir,
	}

	var res acp.SessionNewResult
	if err := g.cli.Call(g.ctx, acp.MethodSessionNew, params, &res); err != nil {
		return nil, fmt.Errorf("create acp session: %w", err)
	}
	if strings.TrimSpace(res.SessionID) != "" {
		sessionID = strings.TrimSpace(res.SessionID)
	}

	state := &telegramGatewaySession{chatID: chatID, sessionID: sessionID}
	g.sessionsMu.Lock()
	g.sessionsByChat[chatID] = state
	g.sessionsByAcpID[sessionID] = state
	g.sessionsMu.Unlock()

	return state, nil
}

func (g *telegramGateway) closeAllSessions() error {
	g.sessionsMu.RLock()
	states := make([]*telegramGatewaySession, 0, len(g.sessionsByAcpID))
	for _, state := range g.sessionsByAcpID {
		states = append(states, state)
	}
	g.sessionsMu.RUnlock()

	for _, state := range states {
		state.mu.Lock()
		sessionID := state.sessionID
		state.mu.Unlock()
		if sessionID == "" {
			continue
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), telegramShutdownTimeout)
		_ = g.cli.Call(closeCtx, acp.MethodSessionClose, struct {
			SessionID string `json:"sessionId"`
		}{SessionID: sessionID}, nil)
		cancel()
	}

	return nil
}

func (g *telegramGateway) sendChunks(chatID int64, chunks []string) error {
	if g.telegramCallFn == nil {
		return fmt.Errorf("telegram call function is not configured")
	}
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if err := g.telegramCallFn("sendMessage", map[string]any{
			"chat_id": chatID,
			"text":    chunk,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (g *telegramGateway) sendChunkToChat(chatID int64, chunk string) error {
	return g.sendChunks(chatID, splitText(chunk))
}

func (g *telegramGateway) sendChatAction(chatID int64) error {
	if g.telegramCallFn == nil {
		return fmt.Errorf("telegram call function is not configured")
	}
	return g.telegramCallFn("sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

func (g *telegramGateway) reactToMessage(chatID int64, messageID int) error {
	if g.telegramCallFn == nil || messageID == 0 {
		return nil
	}
	return g.telegramCallFn("setMessageReaction", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction": []map[string]string{{
			"type":  "emoji",
			"emoji": telegramAckReaction,
		}},
	}, nil)
}

func (g *telegramGateway) telegramCall(method string, params any, out any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(g.ctx, http.MethodPost, telegramAPIBase+g.token+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return redactTelegramTokenError(err, g.token)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram %s failed: status=%d body=%q", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s failed: code=%d desc=%s", method, envelope.ErrorCode, envelope.Description)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return err
		}
	}
	return nil
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int                 `json:"message_id"`
	Text      string              `json:"text"`
	Caption   string              `json:"caption"`
	Photo     []telegramPhotoSize `json:"photo"`
	Voice     *telegramFile       `json:"voice"`
	Audio     *telegramFile       `json:"audio"`
	Chat      telegramChat        `json:"chat"`
}

func (m *telegramMessage) textContent() string {
	if m == nil {
		return ""
	}
	if strings.TrimSpace(m.Text) != "" {
		return m.Text
	}
	return m.Caption
}

// audioFile returns the voice note or audio attachment on the message, if any,
// preferring a voice note.
func (m *telegramMessage) audioFile() *telegramFile {
	if m == nil {
		return nil
	}
	if m.Voice != nil && strings.TrimSpace(m.Voice.FileID) != "" {
		return m.Voice
	}
	if m.Audio != nil && strings.TrimSpace(m.Audio.FileID) != "" {
		return m.Audio
	}
	return nil
}

// photoFile returns the largest available photo size for image prompts.
func (m *telegramMessage) photoFile() *telegramPhotoSize {
	if m == nil || len(m.Photo) == 0 {
		return nil
	}
	best := -1
	bestScore := -1
	for i := range m.Photo {
		if strings.TrimSpace(m.Photo[i].FileID) == "" {
			continue
		}
		score := m.Photo[i].FileSize
		if score <= 0 {
			score = m.Photo[i].Width * m.Photo[i].Height
		}
		if score > bestScore {
			best = i
			bestScore = score
		}
	}
	if best < 0 {
		return nil
	}
	return &m.Photo[best]
}

type telegramFile struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	Duration int    `json:"duration"`
}

type telegramPhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

// errTranscriberUnavailable signals that no transcription command is configured
// or installed, so voice messages cannot be handled.
var errTranscriberUnavailable = errors.New("no transcription command configured")

// transcribeAudioFile runs the configured speech-to-text shim on an audio file
// and returns the transcript. The shim is `$V100_TRANSCRIBE_CMD <file>` (default
// binary: v100-transcribe), mirroring the v100-listen / v100-tts shims: file
// path in, transcript on stdout. When V100_TRANSCRIBE_CMD contains arguments or
// a pipe, the file path is passed safely as $1 via `sh -c`.
func transcribeAudioFile(ctx context.Context, path string) (string, error) {
	raw := strings.TrimSpace(os.Getenv("V100_TRANSCRIBE_CMD"))

	var cmd *exec.Cmd
	if raw == "" {
		if _, err := exec.LookPath("v100-transcribe"); err != nil {
			return "", errTranscriberUnavailable
		}
		cmd = exec.CommandContext(ctx, "v100-transcribe", path)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", raw+` "$1"`, "sh", path)
	}

	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("transcribe: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// downloadAudio resolves a Telegram file_id to a local temp file, returning its
// path. The caller is responsible for removing it.
func (g *telegramGateway) downloadAudio(fileID string) (string, error) {
	var info struct {
		FilePath string `json:"file_path"`
	}
	if err := g.telegramCall("getFile", map[string]any{"file_id": fileID}, &info); err != nil {
		return "", err
	}
	if strings.TrimSpace(info.FilePath) == "" {
		return "", fmt.Errorf("telegram getFile returned empty file_path")
	}

	req, err := http.NewRequestWithContext(g.ctx, http.MethodGet, telegramFileBase+g.token+"/"+info.FilePath, nil)
	if err != nil {
		return "", err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return "", redactTelegramTokenError(err, g.token)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("telegram file download failed: status=%d", resp.StatusCode)
	}

	ext := filepath.Ext(info.FilePath)
	if ext == "" {
		ext = ".oga"
	}
	f, err := os.CreateTemp("", "tg-voice-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, telegramMaxAudioBytes)); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (g *telegramGateway) downloadImage(fileID string) ([]byte, string, error) {
	var info struct {
		FilePath string `json:"file_path"`
	}
	if err := g.telegramCall("getFile", map[string]any{"file_id": fileID}, &info); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(info.FilePath) == "" {
		return nil, "", fmt.Errorf("telegram getFile returned empty file_path")
	}

	req, err := http.NewRequestWithContext(g.ctx, http.MethodGet, telegramFileBase+g.token+"/"+info.FilePath, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, "", redactTelegramTokenError(err, g.token)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("telegram file download failed: status=%d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, telegramMaxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > telegramMaxImageBytes {
		return nil, "", fmt.Errorf("telegram image exceeds %d bytes", telegramMaxImageBytes)
	}

	mimeType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if semi := strings.IndexByte(mimeType, ';'); semi >= 0 {
		mimeType = strings.TrimSpace(mimeType[:semi])
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(filepath.Ext(info.FilePath))
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("telegram file is not an image: %s", mimeType)
	}
	return data, mimeType, nil
}

func (g *telegramGateway) saveTelegramImage(chatID int64, messageID int, index int, img telegramImageAttachment) (string, error) {
	if len(img.Data) == 0 {
		return "", fmt.Errorf("empty image data")
	}
	dir := strings.TrimSpace(g.cfg.Workspace)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	dir = filepath.Join(dir, ".v100-telegram-images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	ext := ".jpg"
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(img.MIMEType)); len(exts) > 0 {
		ext = exts[0]
	}
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
	default:
		ext = ".jpg"
	}
	name := fmt.Sprintf("telegram-%d-%d-%d-%d%s", chatID, messageID, index+1, time.Now().UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, img.Data, 0o600); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// transcribeVoice downloads and transcribes a voice/audio attachment, replying
// to the chat with a clear message on any failure. It returns the transcript, or
// an empty string when nothing actionable was produced.
func (g *telegramGateway) transcribeVoice(chatID int64, file *telegramFile) (string, error) {
	path, err := g.downloadAudio(file.FileID)
	if err != nil {
		_ = g.sendChunks(chatID, []string{"Couldn't download your voice message."})
		return "", err
	}
	defer func() { _ = os.Remove(path) }()

	transcript, err := transcribeAudioFile(g.ctx, path)
	if err != nil {
		if errors.Is(err, errTranscriberUnavailable) {
			_ = g.sendChunks(chatID, []string{"Voice transcription isn't set up on this gateway. Please send text, or configure V100_TRANSCRIBE_CMD."})
		} else {
			_ = g.sendChunks(chatID, []string{"Sorry, I couldn't transcribe that audio."})
		}
		return "", err
	}
	if transcript == "" {
		_ = g.sendChunks(chatID, []string{"I couldn't make out any speech in that message."})
		return "", nil
	}

	// Echo what was understood so the user can catch mis-transcriptions.
	_ = g.sendChunks(chatID, []string{"🎤 " + transcript})
	return transcript, nil
}

func splitText(text string) []string {
	if text == "" {
		return nil
	}
	result := make([]string, 0)
	runes := []rune(text)
	for len(runes) > 0 {
		limit := telegramChunkChars
		if len(runes) < limit {
			limit = len(runes)
		}
		result = append(result, string(runes[:limit]))
		runes = runes[limit:]
	}
	return result
}

func redactTelegramTokenError(err error, token string) error {
	if err == nil {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "<redacted-telegram-token>")
	return errors.New(msg)
}

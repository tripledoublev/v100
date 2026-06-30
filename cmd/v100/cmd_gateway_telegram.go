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
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	gatewaycore "github.com/tripledoublev/v100/internal/gateway"
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
	VoiceReplies    bool
	VoiceReplyMode  string
	StatusInterval  time.Duration
	AllowedChatIDs  map[int64]struct{}
	Provider        string
	Profile         string
	ChatProfiles    map[string]string
	Profiles        map[string]config.GatewayProfile
	PromptBaseDir   string
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
	core       *gatewaycore.Core
	pollOffset int64

	telegramCallFn func(method string, params any, out any) error

	// acpClosed is closed exactly once when the ACP transport drops, so the
	// poll loop can exit instead of prompting against a dead client.
	acpClosed     chan struct{}
	acpClosedOnce sync.Once
}

func (g *telegramGateway) gatewayCore() *gatewaycore.Core {
	if g.core != nil {
		return g.core
	}
	g.core = gatewaycore.NewCore(gatewaycore.Config{
		SessionIDPrefix: "tg-",
		RunDir:          g.cfg.RunDir,
		Workspace:       g.cfg.Workspace,
		StreamResponses: g.cfg.StreamResponses,
		VoiceReplies:    g.cfg.VoiceReplies,
		VoiceReplyMode:  g.cfg.VoiceReplyMode,
		StatusInterval:  g.cfg.StatusInterval,
		PollRetryBase:   telegramPollRetryBase,
		PollRetryMax:    telegramPollRetryMax,
		ChunkChars:      telegramChunkChars,
		BusyMessage:     telegramBusyMessage,
		PrepareSession: func(chatID string, params *acp.SessionNewParams) error {
			return g.applyProfileToSessionParams(chatID, params)
		},
		BuildPrompt: telegramBuildPrompt,
		VoiceSettings: func(chatID string) gatewaycore.VoiceConfig {
			return gatewayVoiceConfig(g.cfg.VoiceReplies, g.cfg.VoiceReplyMode, g.effectiveGatewayProfile(chatID))
		},
	}, g.cli)
	return g.core
}

func (g *telegramGateway) Name() string { return "telegram" }

func (g *telegramGateway) SendText(_ context.Context, chatID string, chunks []string) error {
	id, err := parseTelegramChatID(chatID)
	if err != nil {
		return err
	}
	return g.sendChunks(id, chunks)
}

func (g *telegramGateway) SendVoice(ctx context.Context, chatID, audioPath string) error {
	id, err := parseTelegramChatID(chatID)
	if err != nil {
		return err
	}
	return g.sendVoice(ctx, id, audioPath)
}

func (g *telegramGateway) SendTyping(_ context.Context, chatID string) error {
	id, err := parseTelegramChatID(chatID)
	if err != nil {
		return err
	}
	return g.sendChatAction(id)
}

func (g *telegramGateway) React(_ context.Context, chatID, messageID, emoji string) error {
	id, err := parseTelegramChatID(chatID)
	if err != nil {
		return err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("parse telegram message id %q: %w", messageID, err)
	}
	return g.reactToMessage(id, msgID)
}

func (g *telegramGateway) Allowed(chatID string) bool {
	id, err := parseTelegramChatID(chatID)
	if err != nil {
		return false
	}
	return g.chatAllowed(id)
}

func parseTelegramChatID(chatID string) (int64, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return 0, fmt.Errorf("telegram chat id is required")
	}
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse telegram chat id %q: %w", chatID, err)
	}
	return id, nil
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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-gw.acpClosed:
			cancel()
		case <-runCtx.Done():
		}
	}()
	if err := gw.gatewayCore().Run(runCtx, gw); err != nil {
		return err
	}
	select {
	case <-gw.acpClosed:
		return fmt.Errorf("telegram gateway: ACP connection closed")
	default:
	}
	return nil
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
	normalizedCfg.Profiles = cfg.Gateway.Profiles
	normalizedCfg.PromptBaseDir = cfg.PromptBaseDir()

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
		ctx:            ctx,
		http:           &http.Client{Timeout: telegramHTTPClientTimeout(normalizedCfg.PollTimeout)},
		token:          token,
		cfg:            normalizedCfg,
		telegramCallFn: nil,
		acpClosed:      make(chan struct{}),
	}

	proc, err := gatewaycore.StartACPProcess(ctx, gatewaycore.ACPProcessOptions{
		ConfigPath:      *cfgPath,
		Provider:        normalizedCfg.Provider,
		ShutdownTimeout: telegramShutdownTimeout,
		OnNotification: func(note acp.Notification) {
			_ = gw.handleACPNotification(note)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	gw.cli = proc.Client
	gw.telegramCallFn = gw.telegramCall

	return gw, func() error {
		_ = gw.closeAllSessions()
		return proc.Stop()
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
		VoiceReplies:    cfg.VoiceReplies,
		VoiceReplyMode:  strings.TrimSpace(cfg.VoiceReplyMode),
		StatusInterval:  statusInterval,
		AllowedChatIDs:  allowedChatIDs,
		Provider:        strings.TrimSpace(cfg.Provider),
		Profile:         strings.TrimSpace(cfg.Profile),
		ChatProfiles:    copyStringMap(cfg.ChatProfiles),
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func telegramHTTPClientTimeout(pollTimeoutSeconds int) time.Duration {
	if pollTimeoutSeconds <= 0 {
		return time.Duration(telegramDefaultPollTimeoutSec+10) * time.Second
	}
	return time.Duration(pollTimeoutSeconds+10) * time.Second
}

func (g *telegramGateway) Poll(ctx context.Context) ([]gatewaycore.Update, error) {
	updates, err := g.fetchUpdates()
	if err != nil {
		return nil, err
	}

	out := make([]gatewaycore.Update, 0, len(updates))
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
				if ctx.Err() != nil {
					return nil, nil
				}
				_ = g.sendChunks(chat.ID, []string{"Couldn't download your image."})
				fmt.Fprintf(os.Stderr, "telegram gateway image download error: %v\n", redactTelegramTokenError(perr, g.token))
				continue
			}
			img := telegramImageAttachment{MIMEType: mimeType, Data: data}
			path, werr := g.saveTelegramImage(chat.ID, update.Message.MessageID, len(images), img)
			if werr != nil {
				if ctx.Err() != nil {
					return nil, nil
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
					if ctx.Err() != nil {
						return nil, nil
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
		if command, ok := gatewaycore.ParseCommand(text); ok {
			if err := g.reactToMessage(chat.ID, update.Message.MessageID); err != nil {
				fmt.Fprintf(os.Stderr, "telegram gateway reaction error: %v\n", redactTelegramTokenError(err, g.token))
			}
			if err := g.handleTelegramCommand(chat.ID, command); err != nil {
				if ctx.Err() != nil {
					return nil, nil
				}
				fmt.Fprintf(os.Stderr, "telegram gateway command error: %v\n", redactTelegramTokenError(err, g.token))
				continue
			}
			continue
		}
		out = append(out, gatewaycore.Update{
			ChatID:    strconv.FormatInt(chat.ID, 10),
			MessageID: strconv.Itoa(update.Message.MessageID),
			Text:      text,
			Images:    telegramGatewayImages(images),
		})
	}

	return out, nil
}

func (g *telegramGateway) pollOnce() error {
	// pollOnce is intentionally single-flight: updates are handled sequentially.
	// Concurrency per chat is not supported yet.
	updates, err := g.Poll(g.ctx)
	if err != nil {
		return err
	}
	for _, update := range updates {
		if err := g.gatewayCore().Handle(g.ctx, g, update); err != nil {
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
	return g.gatewayCore().HandleNotification(g.ctx, g, note)
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
	if command, ok := gatewaycore.ParseCommand(text); ok {
		return g.handleTelegramCommand(chatID, command)
	}

	return g.gatewayCore().Handle(g.ctx, g, gatewaycore.Update{
		ChatID: strconv.FormatInt(chatID, 10),
		Text:   text,
		Images: telegramGatewayImages(images),
	})
}

func telegramWorkspacePath(workspace, imagePath string) string {
	return gatewaycore.WorkspacePath(workspace, imagePath)
}

func telegramGatewayImages(images []telegramImageAttachment) []gatewaycore.ImageAttachment {
	if len(images) == 0 {
		return nil
	}
	out := make([]gatewaycore.ImageAttachment, 0, len(images))
	for _, img := range images {
		out = append(out, gatewaycore.ImageAttachment{
			MIMEType: img.MIMEType,
			Data:     img.Data,
			Path:     img.Path,
		})
	}
	return out
}

func telegramBuildPrompt(workspace string, update gatewaycore.Update) []acp.ContentBlock {
	promptText := update.Text
	if len(update.Images) > 0 {
		var b strings.Builder
		b.WriteString(update.Text)
		b.WriteString("\n\nTelegram image attachments were saved as local files for tool use:")
		for i, img := range update.Images {
			if strings.TrimSpace(img.Path) == "" {
				continue
			}
			fmt.Fprintf(&b, "\n- image %d upload path: %s", i+1, img.Path)
			if workspacePath := telegramWorkspacePath(workspace, img.Path); workspacePath != "" {
				fmt.Fprintf(&b, "\n  workspace path: %s", workspacePath)
			}
		}
		b.WriteString("\nIf the user asks to post an attached image to Bluesky, do not ask for a path. Use atproto_upload_blob with the upload path above, then pass the returned cid, mime, size, width, height, and alt to atproto_post images[]. Use the workspace path only for workspace-scoped tools.")
		promptText = b.String()
	}

	prompt := []acp.ContentBlock{{Type: "text", Text: promptText}}
	for _, img := range update.Images {
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
	return prompt
}

func (g *telegramGateway) handleTelegramCommand(chatID int64, command gatewaycore.Command) error {
	if !g.commandAllowed(chatID, command.Name) {
		return g.sendChunkToChat(chatID, fmt.Sprintf("Command /%s is not allowed for this chat.", command.Name))
	}

	switch command.Name {
	case "help":
		return g.sendChunkToChat(chatID, g.telegramCommandHelp(chatID))
	case "whoami":
		return g.sendChunkToChat(chatID, g.telegramCommandWhoami(chatID))
	case "status":
		return g.sendChunkToChat(chatID, g.telegramCommandStatus(chatID))
	case "provider", "model", "solver":
		return g.reconfigureTelegramSession(chatID, command)
	case "profile":
		return g.switchTelegramProfile(chatID, command.Arg)
	case "reset":
		return g.resetTelegramSession(chatID)
	default:
		return g.sendChunkToChat(chatID, fmt.Sprintf("Unknown command /%s. Try /help.", command.Name))
	}
}

func (g *telegramGateway) commandAllowed(chatID int64, command string) bool {
	runtime := g.effectiveGatewayProfile(strconv.FormatInt(chatID, 10))
	return gatewaycore.CommandAllowed(runtime.Profile, strings.TrimSpace(runtime.Name) != "", command)
}

func (g *telegramGateway) telegramCommandHelp(chatID int64) string {
	commands := []string{"help", "whoami", "status", "reset"}
	if runtime := g.effectiveGatewayProfile(strconv.FormatInt(chatID, 10)); runtime.OK {
		commands = runtime.Profile.AllowedCommands
	}
	if len(commands) == 0 {
		return "No commands are enabled for this chat."
	}
	var b strings.Builder
	b.WriteString("Available commands:")
	for _, command := range commands {
		command = gatewaycore.NormalizeCommandName(command)
		if command == "" {
			continue
		}
		fmt.Fprintf(&b, "\n/%s", command)
	}
	return b.String()
}

func (g *telegramGateway) telegramCommandWhoami(chatID int64) string {
	runtime := g.effectiveGatewayProfile(strconv.FormatInt(chatID, 10))
	profileName := runtime.Name
	if !runtime.OK {
		profileName = "(none)"
	}
	profile := runtime.Profile
	sessionID := "(none)"
	if info, ok := g.gatewayCore().SessionInfo(strconv.FormatInt(chatID, 10)); ok {
		sessionID = info.SessionID
	}
	provider := strings.TrimSpace(profile.Provider)
	if provider == "" {
		provider = "(default)"
	}
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		model = "(default)"
	}
	solver := strings.TrimSpace(profile.Solver)
	if solver == "" {
		solver = "(default)"
	}
	return fmt.Sprintf("ChatID: %d\nProfile: %s\nProvider: %s\nModel: %s\nSolver: %s\nTools: %d\nSession: %s", chatID, profileName, provider, model, solver, len(profile.Tools), sessionID)
}

func (g *telegramGateway) telegramCommandStatus(chatID int64) string {
	info, ok := g.gatewayCore().SessionInfo(strconv.FormatInt(chatID, 10))
	if !ok {
		return "No active session for this chat."
	}
	status := "idle"
	if info.InFlight {
		status = "busy"
	}
	last := "never"
	if !info.LastStatus.IsZero() {
		last = info.LastStatus.Format(time.RFC3339)
	}
	return fmt.Sprintf("Status: %s\nSession: %s\nRun dir: %s\nLast activity: %s", status, info.SessionID, g.cfg.RunDir, last)
}

func (g *telegramGateway) reconfigureTelegramSession(chatID int64, command gatewaycore.Command) error {
	res, err := g.gatewayCore().ReconfigureSession(g.ctx, strconv.FormatInt(chatID, 10), command)
	if err != nil {
		if strings.HasPrefix(err.Error(), "usage:") {
			return g.sendChunkToChat(chatID, fmt.Sprintf("Usage: /%s <value>", command.Name))
		}
		return g.sendChunkToChat(chatID, fmt.Sprintf("Reconfigure failed: %v", err))
	}
	if res.SessionID == "" && res.Provider == "" && res.Model == "" && res.Solver == "" {
		return g.sendChunkToChat(chatID, fmt.Sprintf("Usage: /%s <value>", command.Name))
	}
	return g.sendChunkToChat(chatID, fmt.Sprintf("Runtime updated.\nProvider: %s\nModel: %s\nSolver: %s", res.Provider, res.Model, res.Solver))
}

func (g *telegramGateway) switchTelegramProfile(chatID int64, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return g.sendChunkToChat(chatID, "Usage: /profile <name>")
	}
	if _, ok := g.cfg.Profiles[profileName]; !ok {
		return g.sendChunkToChat(chatID, fmt.Sprintf("Unknown profile %q.", profileName))
	}
	if g.cfg.ChatProfiles == nil {
		g.cfg.ChatProfiles = map[string]string{}
	}
	g.cfg.ChatProfiles[strconv.FormatInt(chatID, 10)] = profileName
	if _, err := g.gatewayCore().CloseSession(g.ctx, strconv.FormatInt(chatID, 10)); err != nil {
		return g.sendChunkToChat(chatID, fmt.Sprintf("Profile switch failed: %v", err))
	}
	if _, err := g.gatewayCore().GetOrCreateSession(g.ctx, strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	return g.sendChunkToChat(chatID, fmt.Sprintf("Profile set to %s. Started a fresh session.", profileName))
}

func (g *telegramGateway) resetTelegramSession(chatID int64) error {
	closed, err := g.gatewayCore().CloseSession(g.ctx, strconv.FormatInt(chatID, 10))
	if err != nil {
		return g.sendChunkToChat(chatID, fmt.Sprintf("Reset failed: %v", err))
	}
	if !closed {
		return g.sendChunkToChat(chatID, "No active session to reset.")
	}
	return g.sendChunkToChat(chatID, "Reset complete. The next message will start a fresh session.")
}

func (g *telegramGateway) getOrCreateSession(chatID int64) (*gatewaycore.Session, error) {
	return g.gatewayCore().GetOrCreateSession(g.ctx, strconv.FormatInt(chatID, 10))
}

func (g *telegramGateway) applyProfileToSessionParams(chatID string, params *acp.SessionNewParams) error {
	if g == nil || params == nil {
		return nil
	}
	return gatewaycore.ApplyProfileToSessionNew(params, g.effectiveGatewayProfile(chatID), g.cfg.PromptBaseDir)
}

func (g *telegramGateway) effectiveGatewayProfile(chatID string) gatewaycore.ProfileRuntime {
	if g == nil {
		return gatewaycore.ProfileRuntime{}
	}
	return gatewaycore.ResolveProfile(g.cfg.Profiles, g.cfg.Profile, g.cfg.ChatProfiles, chatID)
}

func (g *telegramGateway) closeAllSessions() error {
	closeCtx, cancel := context.WithTimeout(context.Background(), telegramShutdownTimeout)
	defer cancel()
	return g.gatewayCore().CloseAllSessions(closeCtx)
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

func (g *telegramGateway) sendVoice(ctx context.Context, chatID int64, audioPath string) error {
	audioPath = strings.TrimSpace(audioPath)
	if audioPath == "" {
		return fmt.Errorf("audio path is required")
	}
	info, err := os.Stat(audioPath)
	if err != nil {
		return err
	}
	if info.Size() > telegramMaxAudioBytes {
		return fmt.Errorf("telegram voice reply exceeds %d bytes", telegramMaxAudioBytes)
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("voice", filepath.Base(audioPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telegramAPIBase+g.token+"/sendVoice", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := g.http.Do(req)
	if err != nil {
		return redactTelegramTokenError(err, g.token)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendVoice failed: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("telegram sendVoice failed: code=%d desc=%s", envelope.ErrorCode, envelope.Description)
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
	return gatewaycore.SplitText(text, telegramChunkChars)
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

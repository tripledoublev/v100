package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/acp"
)

const (
	defaultChunkChars     = 3900
	defaultSessionIDPref  = "gw-"
	defaultBusyMessage    = "Still processing previous request. Please wait for my reply first."
	defaultStatusInterval = 2 * time.Second
	defaultPollRetryBase  = 1 * time.Second
	defaultPollRetryMax   = 30 * time.Second
)

// ACPClient is the JSON-RPC client surface Core needs.
type ACPClient interface {
	Call(ctx context.Context, method string, params any, out any) error
}

// Core owns transport-agnostic ACP session lifecycle and response buffering.
type Core struct {
	cfg Config
	cli ACPClient

	sessionsByChat  map[string]*Session
	sessionsByAcpID map[string]*Session
	sessionsMu      sync.RWMutex
}

// Session tracks one chat's ACP session.
type Session struct {
	ChatID     string
	SessionID  string
	InFlight   bool
	Output     strings.Builder
	Stream     strings.Builder
	LastStatus time.Time
	mu         sync.Mutex
}

// SessionInfo is a snapshot of a chat session.
type SessionInfo struct {
	ChatID     string
	SessionID  string
	InFlight   bool
	LastStatus time.Time
}

// NewCore constructs a gateway Core.
func NewCore(cfg Config, cli ACPClient) *Core {
	if cfg.ChunkChars <= 0 {
		cfg.ChunkChars = defaultChunkChars
	}
	if strings.TrimSpace(cfg.SessionIDPrefix) == "" {
		cfg.SessionIDPrefix = defaultSessionIDPref
	}
	if strings.TrimSpace(cfg.BusyMessage) == "" {
		cfg.BusyMessage = defaultBusyMessage
	}
	if cfg.StatusInterval <= 0 {
		cfg.StatusInterval = defaultStatusInterval
	}
	if cfg.PollRetryBase <= 0 {
		cfg.PollRetryBase = defaultPollRetryBase
	}
	if cfg.PollRetryMax <= 0 {
		cfg.PollRetryMax = defaultPollRetryMax
	}
	return &Core{
		cfg:             cfg,
		cli:             cli,
		sessionsByChat:  make(map[string]*Session),
		sessionsByAcpID: make(map[string]*Session),
	}
}

// Run polls a transport and handles each update until ctx is canceled.
func (c *Core) Run(ctx context.Context, t Transport) error {
	if t == nil {
		return fmt.Errorf("gateway transport is required")
	}
	backoff := c.cfg.PollRetryBase
	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := t.Poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < c.cfg.PollRetryMax {
				backoff *= 2
				if backoff > c.cfg.PollRetryMax {
					backoff = c.cfg.PollRetryMax
				}
			}
			continue
		}
		backoff = c.cfg.PollRetryBase
		updates = CoalesceUpdates(updates)
		for _, update := range updates {
			if err := c.Handle(ctx, t, update); err != nil {
				return err
			}
		}
	}
}

// CoalesceUpdates merges multiple pending messages from the same chat into one
// user turn, preserving the order each chat first appeared in the batch.
func CoalesceUpdates(updates []Update) []Update {
	if len(updates) <= 1 {
		return updates
	}
	out := make([]Update, 0, len(updates))
	byChat := make(map[string]int, len(updates))
	for _, update := range updates {
		chatID := strings.TrimSpace(update.ChatID)
		if chatID == "" {
			out = append(out, update)
			continue
		}
		idx, ok := byChat[chatID]
		if !ok {
			update.ChatID = chatID
			byChat[chatID] = len(out)
			out = append(out, update)
			continue
		}
		existing := &out[idx]
		existing.Text = joinUpdateText(existing.Text, update.Text)
		if strings.TrimSpace(update.MessageID) != "" {
			existing.MessageID = update.MessageID
		}
		existing.Images = append(existing.Images, update.Images...)
		if update.Audio != nil {
			existing.Audio = update.Audio
		}
	}
	return out
}

func joinUpdateText(a, b string) string {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n\n" + b
	}
}

// Handle processes one normalized inbound update.
func (c *Core) Handle(ctx context.Context, t Transport, u Update) error {
	if c == nil || c.cli == nil {
		return fmt.Errorf("gateway core is not configured")
	}
	chatID := strings.TrimSpace(u.ChatID)
	if chatID == "" {
		return nil
	}
	if t != nil && !t.Allowed(chatID) {
		return nil
	}


	state, err := c.GetOrCreateSession(ctx, chatID)
	if err != nil {
		return err
	}
	state.mu.Lock()
	if state.InFlight {
		state.mu.Unlock()
		if t == nil {
			return nil
		}
		return t.SendText(ctx, chatID, []string{c.cfg.BusyMessage})
	}
	state.InFlight = true
	state.Output.Reset()
	state.Stream.Reset()
	state.mu.Unlock()

	defer func() {
		state.mu.Lock()
		state.InFlight = false
		state.mu.Unlock()
	}()

	prompt := c.buildPrompt(u)
	var promptRes acp.SessionPromptResult
	if err := c.cli.Call(ctx, acp.MethodSessionPrompt, acp.SessionPromptParams{
		SessionID: state.SessionID,
		Prompt:    prompt,
	}, &promptRes); err != nil {
		if t == nil {
			return err
		}
		return t.SendText(ctx, chatID, []string{fmt.Sprintf("v100 error: %v", err)})
	}
	if c.cfg.StreamResponses {
		if t != nil && !c.voiceConfig(chatID).Enabled {
			if err := c.flushStream(ctx, t, state, true); err != nil {
				return err
			}
		}
		if promptRes.StopReason != "" && promptRes.StopReason != "end_turn" && t != nil {
			return t.SendText(ctx, chatID, []string{fmt.Sprintf("Stopped: %s", promptRes.StopReason)})
		}
		if t != nil && c.voiceConfig(chatID).Enabled {
			state.mu.Lock()
			response := strings.TrimSpace(state.Output.String())
			state.Output.Reset()
			state.mu.Unlock()
			if response != "" {
				textAlreadySent := normalizeVoiceReplyMode(c.voiceConfig(chatID).Mode) == VoiceReplyModeAudioText
				return c.sendReply(ctx, t, chatID, response, textAlreadySent)
			}
		}
		return nil
	}
	state.mu.Lock()
	response := strings.TrimSpace(state.Output.String())
	state.Output.Reset()
	state.mu.Unlock()
	if response == "" {
		response = "(no response)"
	}
	if t == nil {
		return nil
	}
	return c.sendReply(ctx, t, chatID, response, false)
}

// GetOrCreateSession returns the chat's ACP session, creating it when needed.
func (c *Core) GetOrCreateSession(ctx context.Context, chatID string) (*Session, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	c.sessionsMu.Lock()
	existing := c.sessionsByChat[chatID]
	c.sessionsMu.Unlock()
	if existing != nil {
		return existing, nil
	}

	sessionID := c.cfg.SessionIDPrefix + chatID
	params := acp.SessionNewParams{
		SessionID: sessionID,
		CWD:       c.cfg.Workspace,
		RunDir:    c.cfg.RunDir,
	}
	if c.cfg.PrepareSession != nil {
		if err := c.cfg.PrepareSession(chatID, &params); err != nil {
			return nil, err
		}
	}
	var res acp.SessionNewResult
	if err := c.cli.Call(ctx, acp.MethodSessionNew, params, &res); err != nil {
		return nil, fmt.Errorf("create acp session: %w", err)
	}
	if strings.TrimSpace(res.SessionID) != "" {
		sessionID = strings.TrimSpace(res.SessionID)
	}
	state := &Session{ChatID: chatID, SessionID: sessionID}
	c.sessionsMu.Lock()
	c.sessionsByChat[chatID] = state
	c.sessionsByAcpID[sessionID] = state
	c.sessionsMu.Unlock()
	return state, nil
}

// SessionInfo returns a snapshot of the chat session when one exists.
func (c *Core) SessionInfo(chatID string) (SessionInfo, bool) {
	if c == nil {
		return SessionInfo{}, false
	}
	chatID = strings.TrimSpace(chatID)
	c.sessionsMu.RLock()
	state := c.sessionsByChat[chatID]
	c.sessionsMu.RUnlock()
	if state == nil {
		return SessionInfo{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return SessionInfo{
		ChatID:     state.ChatID,
		SessionID:  state.SessionID,
		InFlight:   state.InFlight,
		LastStatus: state.LastStatus,
	}, true
}

// ReconfigureSession applies runtime overrides to an existing chat session.
func (c *Core) ReconfigureSession(ctx context.Context, chatID string, command Command) (acp.SessionReconfigureResult, error) {
	if c == nil || c.cli == nil {
		return acp.SessionReconfigureResult{}, fmt.Errorf("gateway core is not configured")
	}
	state, err := c.GetOrCreateSession(ctx, chatID)
	if err != nil {
		return acp.SessionReconfigureResult{}, err
	}
	params, ok := ReconfigureParams(state.SessionID, command)
	if !ok {
		return acp.SessionReconfigureResult{}, fmt.Errorf("usage: /%s <value>", NormalizeCommandName(command.Name))
	}
	var res acp.SessionReconfigureResult
	if err := c.cli.Call(ctx, acp.MethodSessionReconfigure, params, &res); err != nil {
		return acp.SessionReconfigureResult{}, err
	}
	return res, nil
}

// CloseSession closes and drops the chat's ACP session.
func (c *Core) CloseSession(ctx context.Context, chatID string) (bool, error) {
	if c == nil || c.cli == nil {
		return false, fmt.Errorf("gateway core is not configured")
	}
	chatID = strings.TrimSpace(chatID)
	c.sessionsMu.Lock()
	state := c.sessionsByChat[chatID]
	if state != nil {
		delete(c.sessionsByChat, chatID)
		delete(c.sessionsByAcpID, state.SessionID)
	}
	c.sessionsMu.Unlock()
	if state == nil {
		return false, nil
	}
	state.mu.Lock()
	sessionID := state.SessionID
	state.mu.Unlock()
	if err := c.cli.Call(ctx, acp.MethodSessionClose, map[string]string{"sessionId": sessionID}, nil); err != nil {
		return false, err
	}
	return true, nil
}

// HandleNotification processes ACP session/update notifications.
func (c *Core) HandleNotification(ctx context.Context, t Transport, note acp.Notification) error {
	if note.Method != acp.MethodSessionUpdate {
		return nil
	}
	var update acp.SessionUpdateParams
	if err := json.Unmarshal(note.Params, &update); err != nil {
		return nil
	}
	sessionID := strings.TrimSpace(update.SessionID)
	if sessionID == "" {
		return nil
	}
	c.sessionsMu.RLock()
	state := c.sessionsByAcpID[sessionID]
	c.sessionsMu.RUnlock()
	if state == nil {
		return nil
	}
	switch update.Update.Type {
	case "agent_message_chunk":
		if update.Update.Content == nil || strings.TrimSpace(update.Update.Content.Text) == "" {
			return nil
		}
		if c.cfg.StreamResponses && c.voiceConfig(state.ChatID).Enabled {
			state.mu.Lock()
			state.Output.WriteString(update.Update.Content.Text)
			state.mu.Unlock()
		}
		if c.cfg.StreamResponses {
			if t == nil {
				return nil
			}
			if normalizeVoiceReplyMode(c.voiceConfig(state.ChatID).Mode) == VoiceReplyModeAudio {
				return nil
			}
			state.mu.Lock()
			state.Stream.WriteString(update.Update.Content.Text)
			state.mu.Unlock()
			return c.flushStream(ctx, t, state, false)
		}
		state.mu.Lock()
		state.Output.WriteString(update.Update.Content.Text)
		state.mu.Unlock()
	case "run_status_update":
		if update.Update.Status != "in_progress" {
			return nil
		}
		state.mu.Lock()
		since := time.Since(state.LastStatus)
		state.mu.Unlock()
		if since < c.cfg.StatusInterval || t == nil {
			return nil
		}
		if err := t.SendTyping(ctx, state.ChatID); err != nil {
			return err
		}
		state.mu.Lock()
		state.LastStatus = time.Now()
		state.mu.Unlock()
	case "run_error":
		if strings.TrimSpace(update.Update.Status) == "failed" && t != nil {
			return t.SendText(ctx, state.ChatID, []string{"Run failed. Check the run log for details."})
		}
	}
	return nil
}

// CloseAllSessions closes every known ACP session.
func (c *Core) CloseAllSessions(ctx context.Context) error {
	c.sessionsMu.RLock()
	states := make([]*Session, 0, len(c.sessionsByAcpID))
	for _, state := range c.sessionsByAcpID {
		states = append(states, state)
	}
	c.sessionsMu.RUnlock()
	for _, state := range states {
		state.mu.Lock()
		sessionID := state.SessionID
		state.mu.Unlock()
		if sessionID == "" {
			continue
		}
		_ = c.cli.Call(ctx, acp.MethodSessionClose, struct {
			SessionID string `json:"sessionId"`
		}{SessionID: sessionID}, nil)
	}
	return nil
}

// SplitText splits text into bounded rune chunks.
func SplitText(text string, chunkChars int) []string {
	if chunkChars <= 0 {
		chunkChars = defaultChunkChars
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]string, 0, (len(runes)/chunkChars)+1)
	for len(runes) > chunkChars {
		chunks = append(chunks, string(runes[:chunkChars]))
		runes = runes[chunkChars:]
	}
	chunks = append(chunks, string(runes))
	return chunks
}

func (c *Core) flushStream(ctx context.Context, t Transport, state *Session, final bool) error {
	if t == nil || state == nil {
		return nil
	}
	state.mu.Lock()
	buffer := state.Stream.String()
	flush, rest := splitStreamFlush(buffer, final)
	if flush != "" {
		state.Stream.Reset()
		state.Stream.WriteString(rest)
	}
	state.mu.Unlock()
	flush = strings.TrimSpace(flush)
	if flush == "" {
		return nil
	}
	return t.SendText(ctx, state.ChatID, SplitText(flush, c.cfg.ChunkChars))
}

func splitStreamFlush(buffer string, final bool) (flush, rest string) {
	if strings.TrimSpace(buffer) == "" {
		return "", ""
	}
	if final {
		return buffer, ""
	}
	cut := streamSentenceCut(buffer)
	if cut <= 0 {
		return "", buffer
	}
	return buffer[:cut], buffer[cut:]
}

func streamSentenceCut(s string) int {
	last := -1
	seenText := false
	for i, r := range s {
		if !seenText && !isStreamSpace(r) {
			seenText = true
		}
		if !seenText {
			continue
		}
		if r == '\n' {
			last = i + len(string(r))
			continue
		}
		if !isSentenceTerminator(r) {
			continue
		}
		next := i + len(string(r))
		if next >= len(s) || startsWithSpace(s[next:]) {
			last = next
		}
	}
	return last
}

func isSentenceTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', ':', ';', '…':
		return true
	default:
		return false
	}
}

func startsWithSpace(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		return isStreamSpace(r)
	}
	return true
}

func isStreamSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func (c *Core) buildPrompt(u Update) []acp.ContentBlock {
	if c.cfg.BuildPrompt != nil {
		return c.cfg.BuildPrompt(c.cfg.Workspace, u)
	}
	return updatePrompt(c.cfg.Workspace, u)
}

func (c *Core) voiceConfig(chatID string) VoiceConfig {
	cfg := VoiceConfig{
		Enabled: c.cfg.VoiceReplies,
		Mode:    normalizeVoiceReplyMode(c.cfg.VoiceReplyMode),
	}
	if c.cfg.VoiceSettings != nil {
		override := c.cfg.VoiceSettings(chatID)
		cfg.Enabled = override.Enabled
		if strings.TrimSpace(override.Mode) != "" {
			cfg.Mode = normalizeVoiceReplyMode(override.Mode)
		}
	}
	return cfg
}

func (c *Core) sendReply(ctx context.Context, t Transport, chatID, text string, textAlreadySent bool) error {
	text = strings.TrimSpace(text)
	if text == "" || t == nil {
		return nil
	}
	voice := c.voiceConfig(chatID)
	if !voice.Enabled {
		if textAlreadySent {
			return nil
		}
		return t.SendText(ctx, chatID, SplitText(text, c.cfg.ChunkChars))
	}
	mode := normalizeVoiceReplyMode(voice.Mode)
	audioPath, err := synthesizeReply(ctx, text)
	if err != nil {
		if textAlreadySent {
			return nil
		}
		return t.SendText(ctx, chatID, SplitText(text, c.cfg.ChunkChars))
	}
	if mode == VoiceReplyModeAudioText && !textAlreadySent {
		if err := t.SendText(ctx, chatID, SplitText(text, c.cfg.ChunkChars)); err != nil {
			return err
		}
	}
	return t.SendVoice(ctx, chatID, audioPath)
}

func updatePrompt(workspace string, u Update) []acp.ContentBlock {
	promptText := strings.TrimSpace(u.Text)
	if len(u.Images) > 0 {
		var b strings.Builder
		b.WriteString(promptText)
		b.WriteString("\n\nChat image attachments were saved as local files for tool use:")
		for i, img := range u.Images {
			if strings.TrimSpace(img.Path) == "" {
				continue
			}
			fmt.Fprintf(&b, "\n- image %d upload path: %s", i+1, img.Path)
			if workspacePath := WorkspacePath(workspace, img.Path); workspacePath != "" {
				fmt.Fprintf(&b, "\n  workspace path: %s", workspacePath)
			}
		}
		promptText = b.String()
	}
	prompt := []acp.ContentBlock{{Type: "text", Text: promptText}}
	for _, img := range u.Images {
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

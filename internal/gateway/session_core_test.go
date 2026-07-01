package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/acp"
)

type fakeACPClient struct {
	callCount   atomic.Int32
	newCount    atomic.Int32
	promptCount atomic.Int32

	mu          sync.Mutex
	lastNew     acp.SessionNewParams
	lastPrompt  acp.SessionPromptParams
	lastReconf  acp.SessionReconfigureParams
	lastClose   string
	promptBlock chan struct{}
	onPrompt    func(context.Context)
}

func (c *fakeACPClient) Call(ctx context.Context, method string, params any, out any) error {
	c.callCount.Add(1)
	switch method {
	case acp.MethodSessionNew:
		c.newCount.Add(1)
		if p, ok := params.(acp.SessionNewParams); ok {
			c.mu.Lock()
			c.lastNew = p
			c.mu.Unlock()
		}
		if res, ok := out.(*acp.SessionNewResult); ok {
			if p, ok := params.(acp.SessionNewParams); ok && strings.TrimSpace(p.SessionID) != "" {
				res.SessionID = p.SessionID
			} else {
				res.SessionID = "session"
			}
		}
	case acp.MethodSessionPrompt:
		c.promptCount.Add(1)
		if c.promptBlock != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.promptBlock:
			}
		}
		if p, ok := params.(acp.SessionPromptParams); ok {
			c.mu.Lock()
			c.lastPrompt = p
			c.mu.Unlock()
		}
		if c.onPrompt != nil {
			c.onPrompt(ctx)
		}
		if res, ok := out.(*acp.SessionPromptResult); ok {
			res.StopReason = "end_turn"
		}
	case acp.MethodSessionReconfigure:
		if p, ok := params.(acp.SessionReconfigureParams); ok {
			c.mu.Lock()
			c.lastReconf = p
			c.mu.Unlock()
			if res, ok := out.(*acp.SessionReconfigureResult); ok {
				res.SessionID = p.SessionID
				res.Provider = p.Provider
				res.Model = p.Model
				res.Solver = p.Solver
			}
		}
	case acp.MethodSessionClose:
		switch p := params.(type) {
		case map[string]string:
			c.mu.Lock()
			c.lastClose = p["sessionId"]
			c.mu.Unlock()
		case struct {
			SessionID string `json:"sessionId"`
		}:
			c.mu.Lock()
			c.lastClose = p.SessionID
			c.mu.Unlock()
		}
	}
	return nil
}

type fakeTransport struct {
	allowed map[string]bool
	batches [][]Update
	cancel  context.CancelFunc

	mu        sync.Mutex
	polls     int
	sent      map[string][]string
	voices    map[string][]string
	typing    []string
	reactions []string
}

func (t *fakeTransport) Name() string { return "fake" }

func (t *fakeTransport) Poll(ctx context.Context) ([]Update, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.polls++
	if len(t.batches) == 0 {
		if t.cancel != nil {
			t.cancel()
		}
		return nil, ctx.Err()
	}
	batch := t.batches[0]
	t.batches = t.batches[1:]
	if len(t.batches) == 0 && t.cancel != nil {
		defer t.cancel()
	}
	return batch, nil
}

func (t *fakeTransport) SendText(_ context.Context, chatID string, chunks []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sent == nil {
		t.sent = map[string][]string{}
	}
	t.sent[chatID] = append(t.sent[chatID], chunks...)
	return nil
}

func (t *fakeTransport) SendVoice(_ context.Context, chatID, audioPath string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.voices == nil {
		t.voices = map[string][]string{}
	}
	t.voices[chatID] = append(t.voices[chatID], audioPath)
	return nil
}

func (t *fakeTransport) SendTyping(_ context.Context, chatID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.typing = append(t.typing, chatID)
	return nil
}

func (t *fakeTransport) React(_ context.Context, chatID, messageID, emoji string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reactions = append(t.reactions, chatID+":"+messageID)
	return nil
}

func (t *fakeTransport) Allowed(chatID string) bool {
	if t.allowed == nil {
		return true
	}
	return t.allowed[chatID]
}

func TestCoreCreatesAndReusesSessionPerChat(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{}, cli)
	transport := &fakeTransport{}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", MessageID: "1", Text: "hi"}); err != nil {
		t.Fatalf("first handle returned error: %v", err)
	}
	if err := core.Handle(ctx, transport, Update{ChatID: "42", MessageID: "2", Text: "again"}); err != nil {
		t.Fatalf("second handle returned error: %v", err)
	}
	if got := cli.newCount.Load(); got != 1 {
		t.Fatalf("session/new count = %d, want 1", got)
	}
}

func TestCoreRunPollsTransportAndHandlesUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cli := &fakeACPClient{}
	core := NewCore(Config{PollRetryBase: time.Millisecond, PollRetryMax: time.Millisecond}, cli)
	transport := &fakeTransport{
		cancel: cancel,
		batches: [][]Update{{
			{ChatID: "42", MessageID: "1", Text: "hello"},
		}},
	}
	if err := core.Run(ctx, transport); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := cli.newCount.Load(); got != 1 {
		t.Fatalf("session/new count = %d, want 1", got)
	}
	transport.mu.Lock()
	polls := transport.polls
	transport.mu.Unlock()
	if polls == 0 {
		t.Fatal("expected transport to be polled")
	}
}

func TestCoreRunCoalescesSameChatUpdatesFromPollBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cli := &fakeACPClient{}
	core := NewCore(Config{PollRetryBase: time.Millisecond, PollRetryMax: time.Millisecond}, cli)
	transport := &fakeTransport{
		cancel: cancel,
		batches: [][]Update{{
			{ChatID: "42", MessageID: "1", Text: "first"},
			{ChatID: "42", MessageID: "2", Text: "second"},
		}},
	}
	if err := core.Run(ctx, transport); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := cli.promptCount.Load(); got != 1 {
		t.Fatalf("session/prompt count = %d, want 1", got)
	}
	cli.mu.Lock()
	lastPrompt := cli.lastPrompt
	cli.mu.Unlock()
	if len(lastPrompt.Prompt) != 1 || lastPrompt.Prompt[0].Text != "first\n\nsecond" {
		t.Fatalf("prompt = %#v, want combined text", lastPrompt.Prompt)
	}
}

func TestCoreRunErrorUsesACPErrorDetail(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{}, cli)
	transport := &fakeTransport{}
	state, err := core.GetOrCreateSession(ctx, "42")
	if err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}
	params, err := json.Marshal(acp.SessionUpdateParams{
		SessionID: state.SessionID,
		Update: acp.Update{
			Type:      "run_error",
			Status:    "failed",
			Title:     "run error: provider failed",
			RawOutput: json.RawMessage(`{"error":"provider failed"}`),
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	note := acp.Notification{JSONRPC: "2.0", Method: acp.MethodSessionUpdate, Params: params}
	if err := core.HandleNotification(ctx, transport, note); err != nil {
		t.Fatalf("HandleNotification returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	got := transport.sent["42"]
	if len(got) != 1 || got[0] != "Run failed: provider failed" {
		t.Fatalf("sent messages = %#v", got)
	}
}

func TestCoreRunErrorFallsBackToGenericMessageWhenDetailMissing(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{}, cli)
	transport := &fakeTransport{}
	state, err := core.GetOrCreateSession(ctx, "42")
	if err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}
	params, err := json.Marshal(acp.SessionUpdateParams{
		SessionID: state.SessionID,
		Update: acp.Update{
			Type:      "run_error",
			Status:    "failed",
			Title:     "run error",
			RawOutput: json.RawMessage(`{}`),
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	note := acp.Notification{JSONRPC: "2.0", Method: acp.MethodSessionUpdate, Params: params}
	if err := core.HandleNotification(ctx, transport, note); err != nil {
		t.Fatalf("HandleNotification returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	got := transport.sent["42"]
	if len(got) != 1 || got[0] != "Run failed. Check the run log for details." {
		t.Fatalf("sent messages = %#v", got)
	}
}

func TestCoalesceUpdatesKeepsDifferentChatsSeparate(t *testing.T) {
	got := CoalesceUpdates([]Update{
		{ChatID: "42", MessageID: "1", Text: "first"},
		{ChatID: "99", MessageID: "a", Text: "other"},
		{ChatID: "42", MessageID: "2", Text: "second"},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].ChatID != "42" || got[0].MessageID != "2" || got[0].Text != "first\n\nsecond" {
		t.Fatalf("first coalesced update = %#v", got[0])
	}
	if got[1].ChatID != "99" || got[1].MessageID != "a" || got[1].Text != "other" {
		t.Fatalf("second update = %#v", got[1])
	}
}

func TestCoreStreamingBuffersUntilSentenceBoundary(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{StreamResponses: true}, cli)
	transport := &fakeTransport{}
	cli.onPrompt = func(ctx context.Context) {
		for _, chunk := range []string{"Voici", " une nouvelle", ". Deuxieme", " phrase", "."} {
			if err := core.HandleNotification(ctx, transport, sessionChunkNotification("gw-42", chunk)); err != nil {
				t.Fatalf("HandleNotification returned error: %v", err)
			}
		}
	}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "news"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	got := transport.sent["42"]
	want := []string{"Voici une nouvelle.", "Deuxieme phrase."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sent chunks = %#v, want %#v", got, want)
	}
}

func TestCoreStreamingFlushesRemainderAtEndTurn(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{StreamResponses: true}, cli)
	transport := &fakeTransport{}
	cli.onPrompt = func(ctx context.Context) {
		for _, chunk := range []string{"Pas encore", " une phrase complete"} {
			if err := core.HandleNotification(ctx, transport, sessionChunkNotification("gw-42", chunk)); err != nil {
				t.Fatalf("HandleNotification returned error: %v", err)
			}
		}
	}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "news"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	got := strings.Join(transport.sent["42"], "\n")
	if got != "Pas encore une phrase complete" {
		t.Fatalf("sent = %q", got)
	}
}

func TestCoreVoiceReplyAudioTextSendsTextAndVoice(t *testing.T) {
	ctx := context.Background()
	tts := writeTTSStub(t, true)
	t.Setenv("V100_TTS_CMD", tts)
	cli := &fakeACPClient{}
	core := NewCore(Config{VoiceReplies: true, VoiceReplyMode: VoiceReplyModeAudioText, StreamResponses: false}, cli)
	cli.onPrompt = func(ctx context.Context) {
		sendAgentChunk(t, ctx, core, "gw-42", "Bonjour Quebec.")
	}
	transport := &fakeTransport{}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "salut"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if got := strings.Join(transport.sent["42"], "\n"); got != "Bonjour Quebec." {
		t.Fatalf("sent text = %q", got)
	}
	if len(transport.voices["42"]) != 1 {
		t.Fatalf("voices = %v, want one", transport.voices["42"])
	}
}

func TestCoreVoiceReplyDisabledDoesNotSendVoice(t *testing.T) {
	ctx := context.Background()
	t.Setenv("V100_TTS_CMD", writeTTSStub(t, true))
	cli := &fakeACPClient{}
	core := NewCore(Config{VoiceReplies: false, StreamResponses: false}, cli)
	cli.onPrompt = func(ctx context.Context) {
		sendAgentChunk(t, ctx, core, "gw-42", "plain text")
	}
	transport := &fakeTransport{}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "hi"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.voices["42"]) != 0 {
		t.Fatalf("voices = %v, want none", transport.voices["42"])
	}
	if got := strings.Join(transport.sent["42"], "\n"); got != "plain text" {
		t.Fatalf("sent text = %q", got)
	}
}

func TestCoreVoiceReplyFailureFallsBackToText(t *testing.T) {
	ctx := context.Background()
	t.Setenv("V100_TTS_CMD", writeTTSStub(t, false))
	cli := &fakeACPClient{}
	core := NewCore(Config{VoiceReplies: true, VoiceReplyMode: VoiceReplyModeAudio, StreamResponses: false}, cli)
	cli.onPrompt = func(ctx context.Context) {
		sendAgentChunk(t, ctx, core, "gw-42", "fallback text")
	}
	transport := &fakeTransport{}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "hi"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.voices["42"]) != 0 {
		t.Fatalf("voices = %v, want none", transport.voices["42"])
	}
	if got := strings.Join(transport.sent["42"], "\n"); got != "fallback text" {
		t.Fatalf("sent text = %q", got)
	}
}

func TestCoreVoiceReplyAudioModeSuppressesTextOnSuccess(t *testing.T) {
	ctx := context.Background()
	t.Setenv("V100_TTS_CMD", writeTTSStub(t, true))
	cli := &fakeACPClient{}
	core := NewCore(Config{VoiceReplies: true, VoiceReplyMode: VoiceReplyModeAudio, StreamResponses: false}, cli)
	cli.onPrompt = func(ctx context.Context) {
		sendAgentChunk(t, ctx, core, "gw-42", "voice only")
	}
	transport := &fakeTransport{}

	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "hi"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sent["42"]) != 0 {
		t.Fatalf("sent text = %v, want none", transport.sent["42"])
	}
	if len(transport.voices["42"]) != 1 {
		t.Fatalf("voices = %v, want one", transport.voices["42"])
	}
}

func TestCoreSessionInfoReconfigureAndClose(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{}, cli)
	state, err := core.GetOrCreateSession(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	info, ok := core.SessionInfo("42")
	if !ok || info.SessionID != state.SessionID || info.ChatID != "42" {
		t.Fatalf("SessionInfo = %#v ok=%v", info, ok)
	}
	res, err := core.ReconfigureSession(ctx, "42", Command{Name: "model", Arg: "llama3.1"})
	if err != nil {
		t.Fatalf("ReconfigureSession returned error: %v", err)
	}
	if res.SessionID != state.SessionID || res.Model != "llama3.1" {
		t.Fatalf("reconfigure result = %#v", res)
	}
	cli.mu.Lock()
	reconf := cli.lastReconf
	cli.mu.Unlock()
	if reconf.SessionID != state.SessionID || reconf.Model != "llama3.1" {
		t.Fatalf("reconfigure params = %#v", reconf)
	}
	closed, err := core.CloseSession(ctx, "42")
	if err != nil {
		t.Fatalf("CloseSession returned error: %v", err)
	}
	if !closed {
		t.Fatal("expected session to close")
	}
	if _, ok := core.SessionInfo("42"); ok {
		t.Fatal("session still present after close")
	}
	cli.mu.Lock()
	lastClose := cli.lastClose
	cli.mu.Unlock()
	if lastClose != state.SessionID {
		t.Fatalf("closed session = %q, want %q", lastClose, state.SessionID)
	}
}

func TestCoreBusyMessageWhenChatInFlight(t *testing.T) {
	ctx := context.Background()
	block := make(chan struct{})
	cli := &fakeACPClient{promptBlock: block}
	core := NewCore(Config{BusyMessage: "busy"}, cli)
	transport := &fakeTransport{}

	started := make(chan struct{})
	go func() {
		close(started)
		_ = core.Handle(ctx, transport, Update{ChatID: "42", Text: "first"})
	}()
	<-started
	for i := 0; i < 100; i++ {
		core.sessionsMu.RLock()
		state := core.sessionsByChat["42"]
		core.sessionsMu.RUnlock()
		if state != nil {
			state.mu.Lock()
			inFlight := state.InFlight
			state.mu.Unlock()
			if inFlight {
				break
			}
		}
		time.Sleep(time.Millisecond)
	}
	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "second"}); err != nil {
		t.Fatalf("busy handle returned error: %v", err)
	}
	close(block)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if got := strings.Join(transport.sent["42"], "\n"); !strings.Contains(got, "busy") {
		t.Fatalf("busy message not sent: %q", got)
	}
}

func sendAgentChunk(t *testing.T, ctx context.Context, core *Core, sessionID, text string) {
	t.Helper()
	payload, err := json.Marshal(acp.SessionUpdateParams{
		SessionID: sessionID,
		Update: acp.Update{
			Type: "agent_message_chunk",
			Content: &acp.ContentBlock{
				Type: "text",
				Text: text,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.HandleNotification(ctx, nil, acp.Notification{Method: acp.MethodSessionUpdate, Params: payload}); err != nil {
		t.Fatalf("HandleNotification returned error: %v", err)
	}
}

func writeTTSStub(t *testing.T, ok bool) string {
	t.Helper()
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "reply.ogg")
	if err := os.WriteFile(audioPath, []byte("ogg"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "tts.sh")
	body := "#!/bin/sh\ncat >/dev/null\n"
	if ok {
		body += "printf '%s\\n' " + shellQuote(audioPath) + "\n"
	} else {
		body += "printf 'boom\\n' >&2\nexit 1\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestCoreBufferedResponseConcatenatesAndSplitsChunks(t *testing.T) {
	ctx := context.Background()
	block := make(chan struct{})
	cli := &fakeACPClient{promptBlock: block}
	core := NewCore(Config{ChunkChars: 4}, cli)
	transport := &fakeTransport{}
	done := make(chan error, 1)
	go func() {
		done <- core.Handle(ctx, transport, Update{ChatID: "42", Text: "go"})
	}()
	state := waitCoreSession(t, core, "42")
	if err := core.HandleNotification(ctx, transport, sessionChunkNotification(state.SessionID, "abcd")); err != nil {
		t.Fatalf("first chunk notification returned error: %v", err)
	}
	if err := core.HandleNotification(ctx, transport, sessionChunkNotification(state.SessionID, "ef")); err != nil {
		t.Fatalf("second chunk notification returned error: %v", err)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	transport.mu.Lock()
	got := append([]string(nil), transport.sent["42"]...)
	transport.mu.Unlock()
	if strings.Join(got, "|") != "abcd|ef" {
		t.Fatalf("sent chunks = %v, want [abcd ef]", got)
	}
}

func waitCoreSession(t *testing.T, core *Core, chatID string) *Session {
	t.Helper()
	for i := 0; i < 100; i++ {
		core.sessionsMu.RLock()
		state := core.sessionsByChat[chatID]
		core.sessionsMu.RUnlock()
		if state != nil {
			state.mu.Lock()
			inFlight := state.InFlight
			state.mu.Unlock()
			if inFlight {
				return state
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session %s did not become in-flight", chatID)
	return nil
}

func TestCoreStreamingSendsSentenceFromNotification(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{StreamResponses: true, ChunkChars: 20}, cli)
	transport := &fakeTransport{}
	state, err := core.GetOrCreateSession(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.HandleNotification(ctx, transport, sessionChunkNotification(state.SessionID, "abcdef.")); err != nil {
		t.Fatalf("chunk notification returned error: %v", err)
	}
	transport.mu.Lock()
	got := append([]string(nil), transport.sent["42"]...)
	transport.mu.Unlock()
	if strings.Join(got, "|") != "abcdef." {
		t.Fatalf("stream chunks = %v, want [abcdef.]", got)
	}
}

func TestCoreAllowlistGatesBeforeSession(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{}, cli)
	transport := &fakeTransport{allowed: map[string]bool{"42": false}}
	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "blocked"}); err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	if got := cli.callCount.Load(); got != 0 {
		t.Fatalf("ACP calls = %d, want 0", got)
	}
}

func TestCorePrepareSessionHookMutatesSessionNewParams(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{
		RunDir:    "/tmp/runs",
		Workspace: "/tmp/work",
		PrepareSession: func(chatID string, params *acp.SessionNewParams) error {
			if chatID != "42" {
				t.Fatalf("prepare chatID = %q, want 42", chatID)
			}
			params.Provider = "glm"
			params.Model = "glm-4.6"
			params.Solver = "react"
			params.Tools = []string{"news_fetch"}
			params.Dangerous = []string{}
			return nil
		},
	}, cli)
	if _, err := core.GetOrCreateSession(ctx, "42"); err != nil {
		t.Fatalf("GetOrCreateSession returned error: %v", err)
	}
	if cli.lastNew.Provider != "glm" || cli.lastNew.Model != "glm-4.6" || cli.lastNew.Solver != "react" {
		t.Fatalf("session params = %#v", cli.lastNew)
	}
	if strings.Join(cli.lastNew.Tools, ",") != "news_fetch" {
		t.Fatalf("tools = %v", cli.lastNew.Tools)
	}
	if cli.lastNew.Dangerous == nil || len(cli.lastNew.Dangerous) != 0 {
		t.Fatalf("dangerous = %#v, want explicit empty", cli.lastNew.Dangerous)
	}
}

func TestCoreBuildPromptHookOverridesDefaultPrompt(t *testing.T) {
	ctx := context.Background()
	cli := &fakeACPClient{}
	core := NewCore(Config{
		BuildPrompt: func(workspace string, update Update) []acp.ContentBlock {
			if update.Text != "hello" {
				t.Fatalf("update text = %q, want hello", update.Text)
			}
			return []acp.ContentBlock{{Type: "text", Text: "custom prompt"}}
		},
	}, cli)
	transport := &fakeTransport{}
	if err := core.Handle(ctx, transport, Update{ChatID: "42", Text: "hello"}); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if len(cli.lastPrompt.Prompt) != 1 || cli.lastPrompt.Prompt[0].Text != "custom prompt" {
		t.Fatalf("prompt = %#v", cli.lastPrompt.Prompt)
	}
}

func sessionChunkNotification(sessionID, text string) acp.Notification {
	params, _ := json.Marshal(acp.SessionUpdateParams{
		SessionID: sessionID,
		Update: acp.Update{
			Type: "agent_message_chunk",
			Content: &acp.ContentBlock{
				Type: "text",
				Text: text,
			},
		},
	})
	return acp.Notification{Method: acp.MethodSessionUpdate, Params: params}
}

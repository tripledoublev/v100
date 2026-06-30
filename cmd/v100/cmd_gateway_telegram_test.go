package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	gatewaycore "github.com/tripledoublev/v100/internal/gateway"
)

func TestNormalizeTelegramConfigDefaults(t *testing.T) {
	cfg := config.TelegramConfig{
		BotToken:          "  token  ",
		PollTimeoutSec:    0,
		RunDir:            "  /tmp/v100-run  ",
		Workspace:         "  /tmp/v100-work  ",
		StreamResponses:   true,
		StatusIntervalSec: 0,
	}

	got := normalizeTelegramConfig(cfg)
	if got.PollTimeout != telegramDefaultPollTimeoutSec {
		t.Fatalf("poll timeout = %d, want %d", got.PollTimeout, telegramDefaultPollTimeoutSec)
	}
	if got.RunDir != "/tmp/v100-run" {
		t.Fatalf("run dir = %q, want /tmp/v100-run", got.RunDir)
	}
	if got.Workspace != "/tmp/v100-work" {
		t.Fatalf("workspace = %q, want /tmp/v100-work", got.Workspace)
	}
	if got.StatusInterval != telegramDefaultStatusInterval {
		t.Fatalf("status interval = %s, want %s", got.StatusInterval, telegramDefaultStatusInterval)
	}
	if !got.StreamResponses {
		t.Fatal("expected stream responses to stay enabled")
	}
}

func TestSplitTextSplitsRunesAndBoundedChunks(t *testing.T) {
	text := "😀" + "x"
	chunks := splitText(text)
	if len(chunks) != 1 {
		t.Fatalf("splitText produced %d chunks, want 1", len(chunks))
	}
	if chunks[0] != text {
		t.Fatalf("splitText changed text: %q", chunks[0])
	}

	longText := strings.Repeat("a", telegramChunkChars+17)
	chunks = splitText(longText)
	if len(chunks) != 2 {
		t.Fatalf("splitText produced %d chunks, want 2", len(chunks))
	}
	if len([]rune(chunks[0])) != telegramChunkChars {
		t.Fatalf("first chunk length = %d, want %d", len([]rune(chunks[0])), telegramChunkChars)
	}
	if len([]rune(chunks[1])) != 17 {
		t.Fatalf("second chunk length = %d, want 17", len([]rune(chunks[1])))
	}
}

func TestNormalizeTelegramConfigCapturesAllowedChats(t *testing.T) {
	cfg := config.TelegramConfig{
		PollTimeoutSec:    40,
		StatusIntervalSec: 3,
		AllowedChatIDs:    []int64{111, 222, 111, 0},
	}

	got := normalizeTelegramConfig(cfg)
	if got.PollTimeout != 40 {
		t.Fatalf("poll timeout = %d, want %d", got.PollTimeout, 40)
	}
	if got.StatusInterval != 3*time.Second {
		t.Fatalf("status interval = %s, want %s", got.StatusInterval, 3*time.Second)
	}
	if got.AllowedChatIDs == nil {
		t.Fatal("expected allowed chat IDs map")
	}
	if len(got.AllowedChatIDs) != 2 {
		t.Fatalf("allowed chat ID count = %d, want %d", len(got.AllowedChatIDs), 2)
	}
	if _, ok := got.AllowedChatIDs[111]; !ok {
		t.Fatalf("expected allowed chat id 111")
	}
	if _, ok := got.AllowedChatIDs[222]; !ok {
		t.Fatalf("expected allowed chat id 222")
	}
}

type telegramTestClient struct {
	callCount  atomic.Int32
	getUpdates []telegramUpdate
	lastPrompt acp.SessionPromptParams
}

func (c *telegramTestClient) Call(_ context.Context, method string, params any, out any) error {
	c.callCount.Add(1)
	switch method {
	case acp.MethodSessionNew:
		if res, ok := out.(*acp.SessionNewResult); ok {
			if p, ok := params.(acp.SessionNewParams); ok && strings.TrimSpace(p.SessionID) != "" {
				res.SessionID = p.SessionID
			} else {
				res.SessionID = "tg-session"
			}
		}
	case acp.MethodSessionPrompt:
		if p, ok := params.(acp.SessionPromptParams); ok {
			c.lastPrompt = p
		}
		if res, ok := out.(*acp.SessionPromptResult); ok {
			res.StopReason = "end_turn"
		}
	case "getUpdates":
		if out != nil {
			if updates, ok := out.(*[]telegramUpdate); ok {
				*updates = c.getUpdates
			}
		}
	}
	return nil
}

type telegramSessionCaptureClient struct {
	telegramTestClient
	lastNew             acp.SessionNewParams
	lastReconfigure     acp.SessionReconfigureParams
	lastCloseSessionID  string
	reconfigureResponse acp.SessionReconfigureResult
}

func (c *telegramSessionCaptureClient) Call(ctx context.Context, method string, params any, out any) error {
	if method == acp.MethodSessionNew {
		if p, ok := params.(acp.SessionNewParams); ok {
			c.lastNew = p
		}
	}
	if method == acp.MethodSessionReconfigure {
		if p, ok := params.(acp.SessionReconfigureParams); ok {
			c.lastReconfigure = p
		}
		if res, ok := out.(*acp.SessionReconfigureResult); ok {
			if c.reconfigureResponse.Provider != "" || c.reconfigureResponse.Model != "" || c.reconfigureResponse.Solver != "" {
				*res = c.reconfigureResponse
			} else {
				res.SessionID = c.lastReconfigure.SessionID
				res.Provider = c.lastReconfigure.Provider
				res.Model = c.lastReconfigure.Model
				res.Solver = c.lastReconfigure.Solver
			}
		}
	}
	if method == acp.MethodSessionClose {
		if p, ok := params.(map[string]string); ok {
			c.lastCloseSessionID = p["sessionId"]
		}
	}
	return c.telegramTestClient.Call(ctx, method, params, out)
}

func TestGetOrCreateSessionKeepsRunDirSeparateFromWorkspace(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			RunDir:    "/tmp/v100-runs",
			Workspace: "/tmp/project",
		},
		cli: client,
	}

	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}
	if client.lastNew.RunDir != "/tmp/v100-runs" {
		t.Fatalf("session run dir = %q, want /tmp/v100-runs", client.lastNew.RunDir)
	}
	if client.lastNew.CWD != "/tmp/project" {
		t.Fatalf("session cwd = %q, want /tmp/project", client.lastNew.CWD)
	}

	client = &telegramSessionCaptureClient{}
	gw = &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			RunDir: "/tmp/v100-runs",
		},
		cli: client,
	}

	if _, err := gw.getOrCreateSession(43); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}
	if client.lastNew.RunDir != "/tmp/v100-runs" {
		t.Fatalf("session run dir = %q, want /tmp/v100-runs", client.lastNew.RunDir)
	}
	if client.lastNew.CWD != "" {
		t.Fatalf("session cwd = %q, want empty", client.lastNew.CWD)
	}
}

func TestGetOrCreateSessionAppliesChatProfile(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			RunDir:  "/tmp/v100-runs",
			Profile: "operator",
			ChatProfiles: map[string]string{
				"42": "news_fr",
			},
			Profiles: map[string]config.GatewayProfile{
				"operator": {
					Tools:     []string{"fs_read", "sh"},
					Dangerous: []string{"sh"},
					Provider:  "glm",
				},
				"news_fr": {
					Tools:           []string{"news_fetch", "translate", "sh"},
					Dangerous:       []string{},
					Provider:        "ollama",
					Model:           "llama3.1",
					Solver:          "react",
					SystemPrompt:    "Réponds en français.",
					NetworkTier:     "research",
					BudgetSteps:     12,
					BudgetTokens:    40000,
					BudgetCostUSD:   0.25,
					AllowedCommands: []string{"help", "reset"},
				},
			},
		},
		cli: client,
	}

	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}
	if client.lastNew.Provider != "ollama" || client.lastNew.Model != "llama3.1" || client.lastNew.Solver != "react" {
		t.Fatalf("runtime overrides = %#v", client.lastNew)
	}
	if strings.Join(client.lastNew.Tools, ",") != "news_fetch,translate,sh" {
		t.Fatalf("tools = %v", client.lastNew.Tools)
	}
	if client.lastNew.Dangerous == nil || len(client.lastNew.Dangerous) != 0 {
		t.Fatalf("dangerous = %#v, want explicit empty list", client.lastNew.Dangerous)
	}
	if client.lastNew.SystemPrompt != "Réponds en français." || client.lastNew.NetworkTier != "research" {
		t.Fatalf("prompt/network = %#v", client.lastNew)
	}
	if client.lastNew.BudgetSteps != 12 || client.lastNew.BudgetTokens != 40000 || client.lastNew.BudgetCostUSD != 0.25 {
		t.Fatalf("budgets = %#v", client.lastNew)
	}
}

func TestTelegramCommandHelpHonorsProfileAllowedCommands(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile: "news_fr",
			Profiles: map[string]config.GatewayProfile{
				"news_fr": {
					Tools:           []string{"news_fetch", "translate"},
					AllowedCommands: []string{"help", "reset"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.handleTelegramMessage(42, "/help", nil); err != nil {
		t.Fatalf("handleTelegramMessage returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("help should not call ACP, got %d calls", client.callCount.Load())
	}
	texts := strings.Join(callCapture.sentTexts(), "\n")
	if !strings.Contains(texts, "/help") || !strings.Contains(texts, "/reset") {
		t.Fatalf("help text missing allowed commands: %q", texts)
	}
	if strings.Contains(texts, "/whoami") || strings.Contains(texts, "/status") {
		t.Fatalf("help text included disallowed commands: %q", texts)
	}
}

func TestTelegramCommandRefusesDisallowedProfileCommand(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile: "news_fr",
			Profiles: map[string]config.GatewayProfile{
				"news_fr": {
					Tools:           []string{"news_fetch"},
					AllowedCommands: []string{"help"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.handleTelegramMessage(42, "/reset", nil); err != nil {
		t.Fatalf("handleTelegramMessage returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("disallowed command should not call ACP, got %d calls", client.callCount.Load())
	}
	texts := strings.Join(callCapture.sentTexts(), "\n")
	if !strings.Contains(texts, "not allowed") {
		t.Fatalf("expected refusal, got %q", texts)
	}
}

func TestTelegramCommandRefusesCommandsWhenConfiguredProfileIsMissing(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile:  "missing",
			Profiles: map[string]config.GatewayProfile{},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.handleTelegramMessage(42, "/help", nil); err != nil {
		t.Fatalf("handleTelegramMessage returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("missing profile command should not call ACP, got %d calls", client.callCount.Load())
	}
	texts := strings.Join(callCapture.sentTexts(), "\n")
	if !strings.Contains(texts, "not allowed") {
		t.Fatalf("expected fail-closed refusal, got %q", texts)
	}
}

func TestTelegramCommandWhoamiAndStatusAreLocal(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			RunDir:  "/tmp/runs",
			Profile: "operator",
			Profiles: map[string]config.GatewayProfile{
				"operator": {
					Tools:           []string{"fs_read", "sh"},
					Provider:        "glm",
					Model:           "glm-4.6",
					Solver:          "react",
					AllowedCommands: []string{"whoami", "status"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}
	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}
	client.callCount.Store(0)

	if err := gw.handleTelegramMessage(42, "/whoami", nil); err != nil {
		t.Fatalf("whoami returned error: %v", err)
	}
	if err := gw.handleTelegramMessage(42, "/status", nil); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("local commands should not call ACP, got %d calls", client.callCount.Load())
	}
	texts := strings.Join(callCapture.sentTexts(), "\n")
	for _, want := range []string{"ChatID: 42", "Profile: operator", "Provider: glm", "Session: tg-42", "Status: idle"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("command output missing %q: %q", want, texts)
		}
	}
}

func TestTelegramCommandResetClosesAndDropsSession(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{},
		cli:            client,
		telegramCallFn: callCapture.call,
	}
	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}

	if err := gw.handleTelegramMessage(42, "/reset", nil); err != nil {
		t.Fatalf("handleTelegramMessage returned error: %v", err)
	}
	if client.lastCloseSessionID != "tg-42" {
		t.Fatalf("closed session = %q, want tg-42", client.lastCloseSessionID)
	}
	if _, ok := gw.gatewayCore().SessionInfo("42"); ok {
		t.Fatal("session was not dropped after reset")
	}
	if !strings.Contains(strings.Join(callCapture.sentTexts(), "\n"), "Reset") {
		t.Fatalf("expected reset confirmation, got %v", callCapture.sentTexts())
	}
}

func TestTelegramCommandReconfiguresRuntime(t *testing.T) {
	client := &telegramSessionCaptureClient{
		reconfigureResponse: acp.SessionReconfigureResult{
			SessionID: "tg-42",
			Provider:  "ollama",
			Model:     "llama3.1",
			Solver:    "react",
		},
	}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile: "operator",
			Profiles: map[string]config.GatewayProfile{
				"operator": {
					Tools:           []string{"fs_read"},
					AllowedCommands: []string{"provider", "model", "solver"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}
	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}

	if err := gw.handleTelegramMessage(42, "/provider ollama", nil); err != nil {
		t.Fatalf("provider command returned error: %v", err)
	}
	if client.lastReconfigure.SessionID != "tg-42" || client.lastReconfigure.Provider != "ollama" {
		t.Fatalf("provider reconfigure = %#v", client.lastReconfigure)
	}
	if err := gw.handleTelegramMessage(42, "/model llama3.1", nil); err != nil {
		t.Fatalf("model command returned error: %v", err)
	}
	if client.lastReconfigure.SessionID != "tg-42" || client.lastReconfigure.Model != "llama3.1" {
		t.Fatalf("model reconfigure = %#v", client.lastReconfigure)
	}
	if err := gw.handleTelegramMessage(42, "/solver react", nil); err != nil {
		t.Fatalf("solver command returned error: %v", err)
	}
	if client.lastReconfigure.SessionID != "tg-42" || client.lastReconfigure.Solver != "react" {
		t.Fatalf("solver reconfigure = %#v", client.lastReconfigure)
	}
	texts := strings.Join(callCapture.sentTexts(), "\n")
	if !strings.Contains(texts, "Runtime updated.") || !strings.Contains(texts, "Provider: ollama") {
		t.Fatalf("missing reconfigure confirmation: %q", texts)
	}
}

func TestTelegramCommandProfileSwitchesSandboxAndSession(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile: "operator",
			Profiles: map[string]config.GatewayProfile{
				"operator": {
					Tools:           []string{"fs_read", "sh"},
					Dangerous:       []string{"sh"},
					AllowedCommands: []string{"profile"},
				},
				"news_fr": {
					Tools:           []string{"news_fetch", "translate"},
					Dangerous:       []string{},
					Provider:        "glm",
					Solver:          "react",
					AllowedCommands: []string{"help", "reset"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}
	if _, err := gw.getOrCreateSession(42); err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}

	if err := gw.handleTelegramMessage(42, "/profile news_fr", nil); err != nil {
		t.Fatalf("profile command returned error: %v", err)
	}
	if client.lastCloseSessionID != "tg-42" {
		t.Fatalf("closed session = %q, want tg-42", client.lastCloseSessionID)
	}
	if got := gw.cfg.ChatProfiles["42"]; got != "news_fr" {
		t.Fatalf("chat profile = %q, want news_fr", got)
	}
	if strings.Join(client.lastNew.Tools, ",") != "news_fetch,translate" {
		t.Fatalf("new session tools = %v", client.lastNew.Tools)
	}
	if client.lastNew.Dangerous == nil || len(client.lastNew.Dangerous) != 0 {
		t.Fatalf("new session dangerous = %#v, want explicit empty list", client.lastNew.Dangerous)
	}
	if client.lastNew.Provider != "glm" || client.lastNew.Solver != "react" {
		t.Fatalf("new session runtime = %#v", client.lastNew)
	}
	if !strings.Contains(strings.Join(callCapture.sentTexts(), "\n"), "Profile set to news_fr") {
		t.Fatalf("expected profile confirmation, got %v", callCapture.sentTexts())
	}
}

func TestTelegramCommandProfileRefusesUnknownProfile(t *testing.T) {
	client := &telegramSessionCaptureClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			Profile: "operator",
			Profiles: map[string]config.GatewayProfile{
				"operator": {
					AllowedCommands: []string{"profile"},
				},
			},
		},
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.handleTelegramMessage(42, "/profile missing", nil); err != nil {
		t.Fatalf("profile command returned error: %v", err)
	}
	if client.lastCloseSessionID != "" {
		t.Fatalf("unexpected closed session %q", client.lastCloseSessionID)
	}
	if _, ok := gw.cfg.ChatProfiles["42"]; ok {
		t.Fatalf("unknown profile changed chat profile: %#v", gw.cfg.ChatProfiles)
	}
	if !strings.Contains(strings.Join(callCapture.sentTexts(), "\n"), "Unknown profile") {
		t.Fatalf("expected unknown profile message, got %v", callCapture.sentTexts())
	}
}

type telegramCallCapture struct {
	mu      sync.Mutex
	methods []string
	texts   []string
	params  []map[string]any
}

func (c *telegramCallCapture) call(method string, params any, _ any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods = append(c.methods, method)
	if m, ok := params.(map[string]any); ok {
		c.params = append(c.params, m)
	}
	if method == "sendMessage" {
		if m, ok := params.(map[string]any); ok {
			if text, ok := m["text"].(string); ok {
				c.texts = append(c.texts, text)
			}
		}
	}
	return nil
}

func (c *telegramCallCapture) methodsCalled() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.methods)
}

func (c *telegramCallCapture) calledMethod(method string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, got := range c.methods {
		if got == method {
			return true
		}
	}
	return false
}

func (c *telegramCallCapture) sentTexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.texts))
	copy(out, c.texts)
	return out
}

type telegramUpdateRoundTripper struct {
	mu      sync.Mutex
	updates []telegramUpdate
	calls   int
}

func (r *telegramUpdateRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()

	payload := struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}{
		OK:     true,
		Result: r.updates,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func (r *telegramUpdateRoundTripper) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// telegramVoiceRoundTripper routes by URL path so a voice message can flow
// through getUpdates -> getFile -> file download in one fake transport.
type telegramVoiceRoundTripper struct {
	mu        sync.Mutex
	updates   []telegramUpdate
	filePath  string
	audio     []byte
	getFileN  int
	downloadN int
}

type telegramPhotoRoundTripper struct {
	mu        sync.Mutex
	updates   []telegramUpdate
	filePath  string
	image     []byte
	getFileN  int
	downloadN int
}

type telegramURLLeakRoundTripper struct {
	token string
}

func (r telegramURLLeakRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + req.URL.String())
}

type telegramFileDownloadLeakRoundTripper struct {
	token string
}

type telegramSendVoiceRoundTripper struct {
	mu          sync.Mutex
	called      bool
	contentType string
	body        string
}

func (r telegramFileDownloadLeakRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/getFile") {
		body, _ := json.Marshal(struct {
			OK     bool `json:"ok"`
			Result struct {
				FilePath string `json:"file_path"`
			} `json:"result"`
		}{OK: true, Result: struct {
			FilePath string `json:"file_path"`
		}{FilePath: "voice/file_1.oga"}})
		return jsonResp(body), nil
	}
	return nil, errors.New("download failed for " + req.URL.String())
}

func (r *telegramSendVoiceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/sendVoice") {
		return nil, errors.New("unexpected method " + req.URL.Path)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.called = true
	r.contentType = req.Header.Get("Content-Type")
	r.body = string(body)
	r.mu.Unlock()
	resp, _ := json.Marshal(struct {
		OK     bool `json:"ok"`
		Result bool `json:"result"`
	}{OK: true, Result: true})
	return jsonResp(resp), nil
}

func (r *telegramSendVoiceRoundTripper) snapshot() (bool, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.called, r.contentType, r.body
}

func jsonResp(body []byte) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}

func (r *telegramVoiceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	switch {
	case strings.HasSuffix(p, "/getUpdates"):
		body, _ := json.Marshal(struct {
			OK     bool             `json:"ok"`
			Result []telegramUpdate `json:"result"`
		}{OK: true, Result: r.updates})
		return jsonResp(body), nil
	case strings.HasSuffix(p, "/getFile"):
		r.mu.Lock()
		r.getFileN++
		r.mu.Unlock()
		body, _ := json.Marshal(struct {
			OK     bool `json:"ok"`
			Result struct {
				FilePath string `json:"file_path"`
			} `json:"result"`
		}{OK: true, Result: struct {
			FilePath string `json:"file_path"`
		}{FilePath: r.filePath}})
		return jsonResp(body), nil
	case strings.Contains(p, "/file/bot"):
		r.mu.Lock()
		r.downloadN++
		r.mu.Unlock()
		return jsonResp(r.audio), nil
	default: // sendMessage / sendChatAction
		body, _ := json.Marshal(struct {
			OK     bool `json:"ok"`
			Result bool `json:"result"`
		}{OK: true, Result: true})
		return jsonResp(body), nil
	}
}

func (r *telegramPhotoRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	switch {
	case strings.HasSuffix(p, "/getUpdates"):
		body, _ := json.Marshal(struct {
			OK     bool             `json:"ok"`
			Result []telegramUpdate `json:"result"`
		}{OK: true, Result: r.updates})
		return jsonResp(body), nil
	case strings.HasSuffix(p, "/getFile"):
		r.mu.Lock()
		r.getFileN++
		r.mu.Unlock()
		body, _ := json.Marshal(struct {
			OK     bool `json:"ok"`
			Result struct {
				FilePath string `json:"file_path"`
			} `json:"result"`
		}{OK: true, Result: struct {
			FilePath string `json:"file_path"`
		}{FilePath: r.filePath}})
		return jsonResp(body), nil
	case strings.Contains(p, "/file/bot"):
		r.mu.Lock()
		r.downloadN++
		r.mu.Unlock()
		resp := jsonResp(r.image)
		resp.Header.Set("Content-Type", "image/jpeg")
		return resp, nil
	default: // sendMessage / sendChatAction / setMessageReaction
		body, _ := json.Marshal(struct {
			OK     bool `json:"ok"`
			Result bool `json:"result"`
		}{OK: true, Result: true})
		return jsonResp(body), nil
	}
}

func TestTelegramMessageAudioFilePrefersVoice(t *testing.T) {
	m := &telegramMessage{
		Voice: &telegramFile{FileID: "voice-1"},
		Audio: &telegramFile{FileID: "audio-1"},
	}
	if got := m.audioFile(); got == nil || got.FileID != "voice-1" {
		t.Fatalf("expected voice to win, got %+v", got)
	}

	m = &telegramMessage{Audio: &telegramFile{FileID: "audio-1"}}
	if got := m.audioFile(); got == nil || got.FileID != "audio-1" {
		t.Fatalf("expected audio fallback, got %+v", got)
	}

	m = &telegramMessage{Voice: &telegramFile{FileID: "   "}}
	if got := m.audioFile(); got != nil {
		t.Fatalf("expected blank file_id to be ignored, got %+v", got)
	}

	if (&telegramMessage{Text: "hi"}).audioFile() != nil {
		t.Fatal("text-only message should have no audio file")
	}
}

func TestTelegramMessageTextContentPrefersTextOverCaption(t *testing.T) {
	m := &telegramMessage{Text: " text ", Caption: "caption"}
	if got := m.textContent(); got != " text " {
		t.Fatalf("textContent = %q, want text", got)
	}
	m = &telegramMessage{Caption: "caption"}
	if got := m.textContent(); got != "caption" {
		t.Fatalf("textContent = %q, want caption", got)
	}
}

func TestTelegramMessagePhotoFileSelectsLargest(t *testing.T) {
	m := &telegramMessage{Photo: []telegramPhotoSize{
		{FileID: "small", Width: 100, Height: 100},
		{FileID: "large", Width: 640, Height: 480},
		{FileID: "   ", Width: 9999, Height: 9999},
	}}
	got := m.photoFile()
	if got == nil || got.FileID != "large" {
		t.Fatalf("photoFile = %+v, want large", got)
	}
}

func TestTelegramCallRedactsTokenFromTransportErrors(t *testing.T) {
	token := "123456789:AASecretToken"
	gw := &telegramGateway{
		ctx:   context.Background(),
		http:  &http.Client{Transport: telegramURLLeakRoundTripper{token: token}},
		token: token,
	}

	err := gw.telegramCall("getUpdates", map[string]any{"timeout": 1}, nil)
	if err == nil {
		t.Fatal("expected telegramCall to return transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted-telegram-token>") {
		t.Fatalf("transport error did not include redaction marker: %v", err)
	}
}

func TestDownloadAudioRedactsTokenFromTransportErrors(t *testing.T) {
	token := "123456789:AASecretToken"
	gw := &telegramGateway{
		ctx:   context.Background(),
		http:  &http.Client{Transport: telegramFileDownloadLeakRoundTripper{token: token}},
		token: token,
	}

	_, err := gw.downloadAudio("voice-1")
	if err == nil {
		t.Fatal("expected downloadAudio to return transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("download error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted-telegram-token>") {
		t.Fatalf("download error did not include redaction marker: %v", err)
	}
}

func TestTranscribeAudioFileUnavailableWithoutCommand(t *testing.T) {
	t.Setenv("V100_TRANSCRIBE_CMD", "")
	t.Setenv("PATH", t.TempDir()) // ensure v100-transcribe is not resolvable

	_, err := transcribeAudioFile(context.Background(), "/tmp/whatever.oga")
	if !errors.Is(err, errTranscriberUnavailable) {
		t.Fatalf("expected errTranscriberUnavailable, got %v", err)
	}
}

func TestTranscribeAudioFileRunsConfiguredCommand(t *testing.T) {
	// printf ignores the appended file-path arg (no format directives), so the
	// transcript is exactly the literal.
	t.Setenv("V100_TRANSCRIBE_CMD", "printf 'spoken words'")

	got, err := transcribeAudioFile(context.Background(), "/tmp/whatever.oga")
	if err != nil {
		t.Fatalf("transcribeAudioFile returned error: %v", err)
	}
	if got != "spoken words" {
		t.Fatalf("transcript = %q, want %q", got, "spoken words")
	}
}

func TestPollOnceTranscribesVoiceMessage(t *testing.T) {
	t.Setenv("V100_TRANSCRIBE_CMD", "printf 'turn on the lights'")

	rt := &telegramVoiceRoundTripper{
		updates: []telegramUpdate{{
			UpdateID: 7001,
			Message: &telegramMessage{
				Voice:     &telegramFile{FileID: "voice-abc"},
				MessageID: 44,
				Chat:      telegramChat{ID: 123},
			},
		}},
		filePath: "voice/file_1.oga",
		audio:    []byte("fake-ogg-bytes"),
	}
	client := &telegramTestClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{AllowedChatIDs: map[int64]struct{}{123: {}}, PollTimeout: 1},
		http:           &http.Client{Transport: rt},
		token:          "123:test",
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.pollOnce(); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	if rt.getFileN != 1 || rt.downloadN != 1 {
		t.Fatalf("expected one getFile + one download, got getFile=%d download=%d", rt.getFileN, rt.downloadN)
	}
	// SessionNew + SessionPrompt means the transcript reached the agent.
	if client.callCount.Load() < 2 {
		t.Fatalf("expected ACP session+prompt calls, got %d", client.callCount.Load())
	}
	foundEcho := false
	for _, txt := range callCapture.sentTexts() {
		if strings.Contains(txt, "turn on the lights") {
			foundEcho = true
		}
	}
	if !foundEcho {
		t.Fatalf("expected transcript echo in sent messages, got %v", callCapture.sentTexts())
	}
}

func TestPollOnceForwardsPhotoAsImagePromptAndReacts(t *testing.T) {
	workspace := t.TempDir()
	rt := &telegramPhotoRoundTripper{
		updates: []telegramUpdate{{
			UpdateID: 7101,
			Message: &telegramMessage{
				MessageID: 55,
				Caption:   "post this to bluesky",
				Photo: []telegramPhotoSize{
					{FileID: "photo-small", Width: 90, Height: 90},
					{FileID: "photo-large", Width: 1280, Height: 720, FileSize: 1234},
				},
				Chat: telegramChat{ID: 123},
			},
		}},
		filePath: "photos/file_1.jpg",
		image:    []byte("\xff\xd8\xff\xe0fake-jpeg"),
	}
	client := &telegramTestClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{AllowedChatIDs: map[int64]struct{}{123: {}}, PollTimeout: 1, Workspace: workspace},
		http:           &http.Client{Transport: rt},
		token:          "123:test",
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.pollOnce(); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	if rt.getFileN != 1 || rt.downloadN != 1 {
		t.Fatalf("expected one getFile + one download, got getFile=%d download=%d", rt.getFileN, rt.downloadN)
	}

	if got := client.lastPrompt.Prompt; len(got) != 2 {
		t.Fatalf("prompt blocks = %d, want 2: %#v", len(got), got)
	} else {
		if got[0].Type != "text" || !strings.Contains(got[0].Text, "post this to bluesky") {
			t.Fatalf("unexpected text prompt block: %#v", got[0])
		}
		if !strings.Contains(got[0].Text, "atproto_upload_blob") || !strings.Contains(got[0].Text, ".v100-telegram-images") {
			t.Fatalf("expected prompt to include saved image tool guidance, got: %q", got[0].Text)
		}
		if !strings.Contains(got[0].Text, "upload path: "+workspace) || !strings.Contains(got[0].Text, "workspace path: /workspace/.v100-telegram-images/") {
			t.Fatalf("expected prompt to include upload and workspace paths, got: %q", got[0].Text)
		}
		if !strings.Contains(got[0].Text, "width, height") {
			t.Fatalf("expected prompt to preserve image dimensions, got: %q", got[0].Text)
		}
		if got[1].Type != "image" || got[1].MimeType != "image/jpeg" || got[1].Data == "" {
			t.Fatalf("unexpected image prompt block: %#v", got[1])
		}
	}
	matches, err := filepath.Glob(filepath.Join(workspace, ".v100-telegram-images", "*"))
	if err != nil {
		t.Fatalf("glob saved image: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("saved image count = %d, want 1 in %s", len(matches), workspace)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read saved image: %v", err)
	}
	if string(data) != string(rt.image) {
		t.Fatalf("saved image data = %q, want %q", string(data), string(rt.image))
	}
}

func TestTelegramWorkspacePath(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work")
	got := telegramWorkspacePath(workspace, filepath.Join(workspace, ".v100-telegram-images", "img.jpg"))
	if got != "/workspace/.v100-telegram-images/img.jpg" {
		t.Fatalf("workspace path = %q", got)
	}
	if got := telegramWorkspacePath(workspace, filepath.Join(t.TempDir(), "outside.jpg")); got != "" {
		t.Fatalf("outside path should not be mapped, got %q", got)
	}
}

func TestTelegramBuildPromptKeepsImageToolInstructions(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work")
	imgPath := filepath.Join(workspace, ".v100-telegram-images", "img.jpg")
	got := telegramBuildPrompt(workspace, gatewaycore.Update{
		ChatID: "42",
		Text:   "post this",
		Images: []gatewaycore.ImageAttachment{{
			MIMEType: "image/png",
			Data:     []byte("image-bytes"),
			Path:     imgPath,
		}},
	})
	if len(got) != 2 {
		t.Fatalf("prompt blocks = %d, want 2", len(got))
	}
	for _, want := range []string{
		"Telegram image attachments were saved as local files",
		"/workspace/.v100-telegram-images/img.jpg",
		"atproto_upload_blob",
	} {
		if !strings.Contains(got[0].Text, want) {
			t.Fatalf("prompt text missing %q: %q", want, got[0].Text)
		}
	}
	if got[1].Type != "image" || got[1].MimeType != "image/png" || got[1].Data == "" {
		t.Fatalf("image prompt block = %#v", got[1])
	}
}

func TestTelegramGatewayTransportAdapterUsesStringChatIDs(t *testing.T) {
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		cfg: telegramRuntimeConfig{
			AllowedChatIDs: map[int64]struct{}{42: {}},
		},
		telegramCallFn: callCapture.call,
	}
	if gw.Name() != "telegram" {
		t.Fatalf("transport name = %q", gw.Name())
	}
	if !gw.Allowed("42") || gw.Allowed("99") || gw.Allowed("not-a-chat-id") {
		t.Fatalf("unexpected allowed results")
	}
	if err := gw.SendText(context.Background(), "42", []string{"hello"}); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if err := gw.SendTyping(context.Background(), "42"); err != nil {
		t.Fatalf("SendTyping returned error: %v", err)
	}
	if err := gw.React(context.Background(), "42", "7", "👍"); err != nil {
		t.Fatalf("React returned error: %v", err)
	}
	for _, method := range []string{"sendMessage", "sendChatAction", "setMessageReaction"} {
		if !callCapture.calledMethod(method) {
			t.Fatalf("expected method %s to be called, got %#v", method, callCapture.methods)
		}
	}
	if err := gw.SendText(context.Background(), "invalid", []string{"hello"}); err == nil {
		t.Fatal("expected invalid chat id error")
	}
}

func TestTelegramSendVoiceUploadsAudioFile(t *testing.T) {
	audioPath := filepath.Join(t.TempDir(), "reply.ogg")
	if err := os.WriteFile(audioPath, []byte("fake-ogg"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &telegramSendVoiceRoundTripper{}
	gw := &telegramGateway{
		ctx:   context.Background(),
		http:  &http.Client{Transport: rt},
		token: "123:test",
	}
	if err := gw.SendVoice(context.Background(), "42", audioPath); err != nil {
		t.Fatalf("SendVoice returned error: %v", err)
	}
	called, contentType, body := rt.snapshot()
	if !called {
		t.Fatal("sendVoice was not called")
	}
	if !strings.HasPrefix(contentType, "multipart/form-data;") {
		t.Fatalf("content type = %q", contentType)
	}
	for _, want := range []string{"name=\"chat_id\"", "42", "name=\"voice\"", "reply.ogg", "fake-ogg"} {
		if !strings.Contains(body, want) {
			t.Fatalf("multipart body missing %q: %q", want, body)
		}
	}
}

func TestPollOnceVoiceWithoutTranscriberReplies(t *testing.T) {
	t.Setenv("V100_TRANSCRIBE_CMD", "")
	t.Setenv("PATH", t.TempDir())

	rt := &telegramVoiceRoundTripper{
		updates: []telegramUpdate{{
			UpdateID: 8001,
			Message: &telegramMessage{
				Voice: &telegramFile{FileID: "voice-xyz"},
				Chat:  telegramChat{ID: 123},
			},
		}},
		filePath: "voice/file_2.oga",
		audio:    []byte("fake"),
	}
	client := &telegramTestClient{}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{AllowedChatIDs: map[int64]struct{}{123: {}}, PollTimeout: 1},
		http:           &http.Client{Transport: rt},
		token:          "123:test",
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.pollOnce(); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}

	if client.callCount.Load() != 0 {
		t.Fatalf("expected no ACP calls when transcription is unavailable, got %d", client.callCount.Load())
	}
	foundNotice := false
	for _, txt := range callCapture.sentTexts() {
		if strings.Contains(txt, "isn't set up") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("expected 'not set up' notice, got %v", callCapture.sentTexts())
	}
}

func TestHandleTelegramMessageRejectsDisallowedChat(t *testing.T) {
	client := &telegramTestClient{}
	gw := &telegramGateway{
		ctx: context.Background(),
		cfg: telegramRuntimeConfig{
			AllowedChatIDs: map[int64]struct{}{
				123: {},
			},
			StreamResponses: true,
			StatusInterval:  time.Second,
		},
		cli: client,
	}

	if err := gw.handleTelegramMessage(999, "should be ignored", nil); err != nil {
		t.Fatalf("handleTelegramMessage returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("expected telegram client to be untouched for disallowed chat, got %d calls", client.callCount.Load())
	}
}

func TestPollOnceRejectsDisallowedChat(t *testing.T) {
	client := &telegramTestClient{
		getUpdates: []telegramUpdate{
			{
				UpdateID: 9001,
				Message: &telegramMessage{
					Text: "should be ignored",
					Chat: telegramChat{ID: 999},
				},
			},
		},
	}

	rt := &telegramUpdateRoundTripper{
		updates: client.getUpdates,
	}
	callCapture := &telegramCallCapture{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{AllowedChatIDs: map[int64]struct{}{123: {}}, PollTimeout: 1, StreamResponses: true},
		http:           &http.Client{Transport: rt},
		token:          "test",
		cli:            client,
		telegramCallFn: callCapture.call,
	}

	if err := gw.pollOnce(); err != nil {
		t.Fatalf("pollOnce returned error: %v", err)
	}
	if client.callCount.Load() != 0 {
		t.Fatalf("expected disallowed chat to skip ACP calls, got %d", client.callCount.Load())
	}
	if callCapture.methodsCalled() != 0 {
		t.Fatalf("expected no Telegram send methods to be called, got %d", callCapture.methodsCalled())
	}
	if rt.callCount() != 1 {
		t.Fatalf("expected one getUpdates request, got %d", rt.callCount())
	}
}

func TestHandleACPNotificationRunErrorNotifiesInNonStreamingMode(t *testing.T) {
	callCapture := &telegramCallCapture{}
	client := &telegramSessionCaptureClient{}
	gw := &telegramGateway{
		ctx:            context.Background(),
		cfg:            telegramRuntimeConfig{StreamResponses: false},
		cli:            client,
		telegramCallFn: callCapture.call,
	}
	state, err := gw.getOrCreateSession(42)
	if err != nil {
		t.Fatalf("getOrCreateSession returned error: %v", err)
	}

	params, err := json.Marshal(acp.SessionUpdateParams{
		SessionID: state.SessionID,
		Update: acp.Update{
			Type:   "run_error",
			Status: "failed",
		},
	})
	if err != nil {
		t.Fatalf("marshal session update: %v", err)
	}
	note := acp.Notification{
		JSONRPC: "2.0",
		Method:  acp.MethodSessionUpdate,
		Params:  params,
	}
	if err := gw.handleACPNotification(note); err != nil {
		t.Fatalf("handleACPNotification returned error: %v", err)
	}

	msgs := callCapture.sentTexts()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(msgs))
	}
	if msgs[0] != "Run failed. Check the run log for details." {
		t.Fatalf("unexpected message: %q", msgs[0])
	}
}

func TestHandleACPNotificationConnectionClosedSignals(t *testing.T) {
	gw := &telegramGateway{
		ctx:       context.Background(),
		acpClosed: make(chan struct{}),
	}

	note := acp.Notification{JSONRPC: "2.0", Method: acp.MethodConnectionClosed}
	if err := gw.handleACPNotification(note); err != nil {
		t.Fatalf("handleACPNotification returned error: %v", err)
	}

	select {
	case <-gw.acpClosed:
	default:
		t.Fatal("expected acpClosed channel to be closed after connection/closed notification")
	}

	// Idempotent: a second notification must not panic on a re-close.
	if err := gw.handleACPNotification(note); err != nil {
		t.Fatalf("second handleACPNotification returned error: %v", err)
	}
}

func TestHandleACPNotificationDropsMalformedPayload(t *testing.T) {
	gw := &telegramGateway{
		ctx: context.Background(),
	}
	note := acp.Notification{
		JSONRPC: "2.0",
		Method:  acp.MethodSessionUpdate,
		Params:  json.RawMessage(`{"sessionId": 12345}`), // wrong type, fails to unmarshal
	}
	if err := gw.handleACPNotification(note); err != nil {
		t.Fatalf("expected malformed payload to be dropped without error, got %v", err)
	}
}

func TestNormalizeTelegramConfigClampsTimingUpperBounds(t *testing.T) {
	got := normalizeTelegramConfig(config.TelegramConfig{
		PollTimeoutSec:    9000,
		StatusIntervalSec: 9000,
	})
	if got.PollTimeout != telegramMaxPollTimeoutSec {
		t.Fatalf("poll timeout = %d, want clamp to %d", got.PollTimeout, telegramMaxPollTimeoutSec)
	}
	want := time.Duration(telegramMaxStatusIntervalSec) * time.Second
	if got.StatusInterval != want {
		t.Fatalf("status interval = %s, want clamp to %s", got.StatusInterval, want)
	}
}

func TestSetupTelegramGatewayRejectsMalformedToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	configFile := `[telegram]
enabled = true
bot_token = "not a valid token with spaces"
`
	if err := os.WriteFile(path, []byte(configFile), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := setupTelegramGateway(ctx, &path)
	if err == nil {
		t.Fatal("expected gateway setup to reject a malformed bot token")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("expected malformed-token error, got %v", err)
	}
}

func TestTelegramTokenPatternAcceptsRealisticToken(t *testing.T) {
	valid := []string{
		"123456789:AAFakeTokenValue_-09",
		"1:A",
	}
	for _, tok := range valid {
		if !telegramTokenPattern.MatchString(tok) {
			t.Errorf("expected %q to be accepted", tok)
		}
	}
	invalid := []string{
		"",
		"missingcolon",
		"123:bad token",
		"123:tok\nen",
		"abc:123",
	}
	for _, tok := range invalid {
		if telegramTokenPattern.MatchString(tok) {
			t.Errorf("expected %q to be rejected", tok)
		}
	}
}

func TestTelegramHTTPClientTimeoutDerivesFromPollTimeout(t *testing.T) {
	cfg := normalizeTelegramConfig(config.TelegramConfig{
		PollTimeoutSec: 17,
	})
	got := telegramHTTPClientTimeout(cfg.PollTimeout)
	want := 27 * time.Second
	if got != want {
		t.Fatalf("http timeout = %v, want %v", got, want)
	}

	cfgZero := normalizeTelegramConfig(config.TelegramConfig{})
	got = telegramHTTPClientTimeout(cfgZero.PollTimeout)
	want = (telegramDefaultPollTimeoutSec + 10) * time.Second
	if got != want {
		t.Fatalf("defaulted http timeout = %v, want %v", got, want)
	}
}

func TestGatewayCmdHasTelegramSubcommand(t *testing.T) {
	cfgPath := "config.toml"
	cmd := gatewayCmd(&cfgPath)
	children := cmd.Commands()
	names := map[string]bool{}
	for _, c := range children {
		names[c.Name()] = true
	}
	if len(children) != 2 || !names["telegram"] || !names["signal"] {
		t.Fatalf("gateway command children = %v", func() []string {
			out := make([]string, 0, len(children))
			for _, c := range children {
				out = append(out, c.Name())
			}
			return out
		}())
	}

	var telegramCmd *cobra.Command
	for _, c := range children {
		if c.Name() == "telegram" {
			telegramCmd = c
			break
		}
	}
	if telegramCmd == nil || telegramCmd.Flags().Lookup("once") == nil {
		t.Fatal("expected telegram command to expose --once flag")
	}
}

func TestGatewayPollingRetryCaps(t *testing.T) {
	if telegramPollRetryBase > telegramPollRetryMax {
		t.Fatalf("poll retry base (%s) must be <= max (%s)", telegramPollRetryBase, telegramPollRetryMax)
	}
	if telegramPollRetryBase <= 0 || telegramPollRetryMax <= 0 {
		t.Fatalf("poll retry durations must be positive: base=%s max=%s", telegramPollRetryBase, telegramPollRetryMax)
	}
}

func TestSplitTextNil(t *testing.T) {
	if chunks := splitText(""); len(chunks) != 0 {
		t.Fatalf("splitText(\"\") = %#v", chunks)
	}
}

func TestSetupTelegramGatewayReturnsDisabledWithoutToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	configFile := `[telegram]
enabled = true
bot_token = ""
bot_token_env = "V100_TELEGRAM_TOKEN_GW_TEST"
`
	if err := os.WriteFile(path, []byte(configFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ensure this env var is unset for the test process to avoid accidental success.
	t.Setenv("V100_TELEGRAM_TOKEN_GW_TEST", "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, _, err := setupTelegramGateway(ctx, &path); err == nil {
		t.Fatal("expected gateway setup to fail when token is missing")
	}
}

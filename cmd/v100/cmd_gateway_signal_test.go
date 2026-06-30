package main

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	gatewaycore "github.com/tripledoublev/v100/internal/gateway"
)

type fakeSignalRPC struct {
	mu       sync.Mutex
	receives []signalReceiveEnvelope
	calls    []signalRPCCall
}

type signalRPCCall struct {
	method string
	params any
}

type fakeSignalACPClient struct {
	mu         sync.Mutex
	lastNew    acp.SessionNewParams
	lastPrompt acp.SessionPromptParams
}

func (f *fakeSignalACPClient) Call(_ context.Context, method string, params any, out any) error {
	switch method {
	case acp.MethodSessionNew:
		if p, ok := params.(acp.SessionNewParams); ok {
			f.mu.Lock()
			f.lastNew = p
			f.mu.Unlock()
			if res, ok := out.(*acp.SessionNewResult); ok {
				res.SessionID = p.SessionID
			}
		}
	case acp.MethodSessionPrompt:
		if p, ok := params.(acp.SessionPromptParams); ok {
			f.mu.Lock()
			f.lastPrompt = p
			f.mu.Unlock()
		}
		if res, ok := out.(*acp.SessionPromptResult); ok {
			res.StopReason = "end_turn"
		}
	}
	return nil
}

func (f *fakeSignalRPC) Receive(context.Context) ([]signalReceiveEnvelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]signalReceiveEnvelope(nil), f.receives...)
	f.receives = nil
	return out, nil
}

func (f *fakeSignalRPC) Call(_ context.Context, method string, params any, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, signalRPCCall{method: method, params: params})
	return nil
}

func TestSignalPollConvertsAllowedReceiveToGatewayUpdate(t *testing.T) {
	rpc := &fakeSignalRPC{receives: []signalReceiveEnvelope{{
		Envelope: signalEnvelope{
			Source: "+15145550000",
			DataMessage: &signalDataMessage{
				Message: "bonjour",
			},
		},
	}}}
	gw := &signalGateway{globalCfg: config.DefaultConfig(),
		cfg: signalRuntimeConfig{
			AllowedNumbers: map[string]struct{}{"+15145550000": {}},
		},
		rpc: rpc,
	}

	updates, err := gw.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	if updates[0].ChatID != "+15145550000" || updates[0].Text != "bonjour" {
		t.Fatalf("update = %#v", updates[0])
	}
}

func TestSignalPollDropsDisallowedReceive(t *testing.T) {
	rpc := &fakeSignalRPC{receives: []signalReceiveEnvelope{{
		Envelope: signalEnvelope{
			Source: "+15145550000",
			DataMessage: &signalDataMessage{
				Message: "bonjour",
			},
		},
	}}}
	gw := &signalGateway{globalCfg: config.DefaultConfig(),
		cfg: signalRuntimeConfig{
			AllowedNumbers: map[string]struct{}{"+15145559999": {}},
		},
		rpc: rpc,
	}
	updates, err := gw.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %#v, want none", updates)
	}
}

func TestSignalSendTextTypingAndReaction(t *testing.T) {
	rpc := &fakeSignalRPC{}
	gw := &signalGateway{globalCfg: config.DefaultConfig(),
		cfg: signalRuntimeConfig{Account: "+15145551234"},
		rpc: rpc,
	}
	if err := gw.SendText(context.Background(), "+15145550000", []string{"hello", "again"}); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if err := gw.SendTyping(context.Background(), "+15145550000"); err != nil {
		t.Fatalf("SendTyping returned error: %v", err)
	}
	if err := gw.React(context.Background(), "+15145550000", "123", "👍"); err != nil {
		t.Fatalf("React returned error: %v", err)
	}
	got := []string{}
	for _, call := range rpc.calls {
		got = append(got, call.method)
	}
	if strings.Join(got, ",") != "send,send,sendTyping,sendReaction" {
		t.Fatalf("methods = %v", got)
	}
}

func TestRedactSignalAccountError(t *testing.T) {
	err := redactSignalAccountError(assertErr("+15145551234 failed"), "+15145551234")
	if strings.Contains(err.Error(), "+15145551234") || !strings.Contains(err.Error(), "<redacted-signal-account>") {
		t.Fatalf("redacted error = %v", err)
	}
}

func TestSignalJSONRPCReceive(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	rpc := &signalJSONRPC{conn: clientConn, account: "+15145551234"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var req map[string]any
		dec := json.NewDecoder(serverConn)
		if err := dec.Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req["method"] != "receive" {
			t.Errorf("method = %v, want receive", req["method"])
		}
		params, ok := req["params"].(map[string]any)
		if !ok {
			t.Errorf("params = %#v, want object", req["params"])
		} else if params["maxMessages"] != float64(100) {
			t.Errorf("maxMessages = %#v, want 100", params["maxMessages"])
		}
		res := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": []map[string]any{{
				"envelope": map[string]any{
					"source": "+15145550000",
					"dataMessage": map[string]any{
						"message": "bonjour",
					},
				},
			}},
		}
		if err := json.NewEncoder(serverConn).Encode(res); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}()
	got, err := rpc.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	<-done
	if len(got) != 1 || got[0].Envelope.Source != "+15145550000" || got[0].Envelope.DataMessage.Message != "bonjour" {
		t.Fatalf("receive = %#v", got)
	}
}

func TestSignalGatewayImplementsTransport(t *testing.T) {
	var _ gatewaycore.Transport = (*signalGateway)(nil)
}

func TestSignalVincentPresetSessionUsesProfileAndChatPrompt(t *testing.T) {
	presetPath := filepath.Join("..", "..", "docs", "examples", "signal-chat-fr", "config.toml")
	cfg, err := config.Load(presetPath)
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Gateway.Profiles["signal-vincent"]
	rpc := &fakeSignalRPC{receives: []signalReceiveEnvelope{{
		Envelope: signalEnvelope{
			Source:    "+1XXXXXXXXXX",
			Timestamp: "1",
			DataMessage: &signalDataMessage{
				Message: "salut",
			},
		},
	}}}
	cli := &fakeSignalACPClient{}
	gw := &signalGateway{globalCfg: config.DefaultConfig(),
		cfg: signalRuntimeConfig{
			Account:        cfg.Signal.Account,
			AllowedNumbers: map[string]struct{}{"+1XXXXXXXXXX": {}},
			Profile:        "signal-vincent",
			Profiles:       cfg.Gateway.Profiles,
			PromptBaseDir:  cfg.PromptBaseDir(),
		},
		rpc: rpc,
		cli: cli,
	}

	updates, err := gw.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %#v, want one", updates)
	}
	if err := gw.gatewayCore().Handle(context.Background(), gw, updates[0]); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	cli.mu.Lock()
	lastNew := cli.lastNew
	lastPrompt := cli.lastPrompt
	cli.mu.Unlock()
	if strings.Join(lastNew.Tools, ",") != strings.Join(profile.Tools, ",") {
		t.Fatalf("session tools = %v, want %v", lastNew.Tools, profile.Tools)
	}
	if len(lastNew.Dangerous) != 0 || containsString(lastNew.Tools, "sh") || containsString(lastNew.Tools, "git_commit") || containsString(lastNew.Tools, "atproto_post") || containsString(lastNew.Tools, "news_fetch") {
		t.Fatalf("unsafe session sandbox: tools=%v dangerous=%v", lastNew.Tools, lastNew.Dangerous)
	}
	if !strings.Contains(lastNew.SystemPrompt, "Tu es Vincent") || !strings.Contains(lastNew.SystemPrompt, "chat perso") {
		t.Fatalf("system prompt did not include Vincent chat persona: %q", lastNew.SystemPrompt)
	}
	if cfg.ATProto.Handle != "your-handle.bsky.social" || cfg.ATProto.AppPasswordEnv != "V100_BSKY_APP_PASSWORD" {
		t.Fatalf("atproto config = %+v", cfg.ATProto)
	}
	if lastNew.NetworkTier != "research" || lastNew.BudgetSteps != 20 || lastNew.BudgetTokens != 40000 {
		t.Fatalf("runtime profile params = %#v", lastNew)
	}
	if lastPrompt.SessionID == "" || len(lastPrompt.Prompt) == 0 || lastPrompt.Prompt[0].Text != "salut" {
		t.Fatalf("prompt params = %#v", lastPrompt)
	}
}

func TestSignalVincentPresetDoesNotDefaultToNewsFetch(t *testing.T) {
	presetPath := filepath.Join("..", "..", "docs", "examples", "signal-chat-fr", "config.toml")
	cfg, err := config.Load(presetPath)
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Gateway.Profiles["signal-vincent"]
	for _, tool := range profile.Tools {
		if tool == "news_fetch" {
			t.Fatal("signal-vincent profile should not include news_fetch")
		}
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

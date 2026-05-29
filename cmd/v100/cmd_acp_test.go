package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
)

func TestACPLifecycleInitializeSuggestedPromptsFinalize(t *testing.T) {
	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
		cfgPath:  filepath.Join(t.TempDir(), "config.toml"),
	}

	server.handleRequest(acpRequest(t, 1, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      acp.ClientInfo{Name: "test-client", Version: "1.0.0"},
		ClientCapabilities: acp.ClientCapabilities{
			Terminal: true,
			FS:       map[string]bool{"readTextFile": true},
		},
	}))
	responses := acpResponses(t, out.String())
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	var initResult acp.InitializeResult
	decodeACPResult(t, responses[0], &initResult)
	if initResult.ProtocolVersion != acp.ProtocolVersion || initResult.AgentInfo.Name != "v100" {
		t.Fatalf("initialize result = %#v", initResult)
	}
	if !server.initialized || server.clientInfo.Name != "test-client" || !server.clientCaps.Terminal {
		t.Fatalf("server handshake state not recorded: %#v %#v", server.clientInfo, server.clientCaps)
	}

	prompts := []acp.SuggestedPrompt{{
		ID:     "fix",
		Title:  "Fix failing tests",
		Prompt: "Run the focused test and repair the failing assertion.",
		Tags:   []string{"test"},
	}}
	server.handleRequest(acpRequest(t, 2, acp.MethodSetSuggestedPrompts, acp.SetSuggestedPromptsParams{
		Prompts: prompts,
	}))
	responses = acpResponses(t, out.String())
	var setGlobal acp.SetSuggestedPromptsResult
	decodeACPResult(t, responses[1], &setGlobal)
	if setGlobal.Count != 1 || len(server.suggestedPrompts) != 1 {
		t.Fatalf("global prompts not stored: result=%#v prompts=%#v", setGlobal, server.suggestedPrompts)
	}

	trace, err := core.OpenTrace(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	fakeSession := &fakeACPSession{}
	server.sessions["session-1"] = &acpSession{
		comp: &RunComponents{
			Config:  &config.Config{Sandbox: config.SandboxConfig{Enabled: true}},
			Trace:   trace,
			Session: fakeSession,
		},
		prompts: copySuggestedPrompts(server.suggestedPrompts),
	}
	server.handleRequest(acpRequest(t, 3, acp.MethodSetSuggestedPrompts, acp.SetSuggestedPromptsParams{
		SessionID: "session-1",
		Prompts:   prompts,
	}))
	if got := server.sessions["session-1"].prompts[0].Title; got != "Fix failing tests" {
		t.Fatalf("session prompt title = %q", got)
	}

	server.handleRequest(acpRequest(t, 4, acp.MethodFinalize, acp.FinalizeParams{Reason: "test"}))
	responses = acpResponses(t, out.String())
	var finalized acp.FinalizeResult
	decodeACPResult(t, responses[3], &finalized)
	if finalized.ClosedSessions != 1 {
		t.Fatalf("finalize result = %#v", finalized)
	}
	if !fakeSession.closed {
		t.Fatal("finalize did not close sandbox session")
	}
	if server.initialized || len(server.sessions) != 0 || len(server.suggestedPrompts) != 0 {
		t.Fatalf("server not finalized: initialized=%v sessions=%d prompts=%d", server.initialized, len(server.sessions), len(server.suggestedPrompts))
	}
}

func TestACPServeStdioLifecycleFinalizeStopsServer(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[providers.llamacpp]
type = "llamacpp"
default_model = "test-model"
base_url = "http://127.0.0.1:19091/v1"

[embedding]
provider = "llamacpp"
model = "test-model"

[defaults]
provider = "llamacpp"
cheap_provider = "llamacpp"
smart_provider = "llamacpp"
compress_provider = "llamacpp"
confirm_tools = "dangerous"
budget_steps = 1
budget_tokens = 1000
max_tool_calls_per_step = 1
`), 0o644); err != nil {
		t.Fatal(err)
	}

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	server := &acpServer{
		conn:     acp.NewConn(inReader, outWriter),
		sessions: make(map[string]*acpSession),
		cfgPath:  cfgPath,
		cmd:      &cobra.Command{},
	}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.serve()
	}()
	defer func() {
		_ = inWriter.Close()
		_ = outReader.Close()
		_ = outWriter.Close()
	}()

	scanner := bufio.NewScanner(outReader)

	writeACPRequest(t, inWriter, 1, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion + 1,
		ClientInfo:      acp.ClientInfo{Name: "stdio-test", Version: "1.0.0"},
		ClientCapabilities: acp.ClientCapabilities{
			Terminal: true,
			FS:       map[string]bool{"readTextFile": true, "writeTextFile": true},
		},
	})
	var initResult acp.InitializeResult
	readACPResponse(t, scanner, 1, &initResult)
	if initResult.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("negotiated protocol version = %d, want %d", initResult.ProtocolVersion, acp.ProtocolVersion)
	}

	writeACPRequest(t, inWriter, 2, acp.MethodSetSuggestedPrompts, acp.SetSuggestedPromptsParams{
		Prompts: []acp.SuggestedPrompt{{
			ID:     "orient",
			Title:  "Orient",
			Prompt: "Summarize the workspace.",
		}},
	})
	var prompts acp.SetSuggestedPromptsResult
	readACPResponse(t, scanner, 2, &prompts)
	if prompts.Count != 1 {
		t.Fatalf("prompt count = %d, want 1", prompts.Count)
	}

	writeACPRequest(t, inWriter, 3, acp.MethodSessionNew, acp.SessionNewParams{CWD: workDir})
	var session acp.SessionNewResult
	readACPResponse(t, scanner, 3, &session)
	if session.SessionID == "" {
		t.Fatal("empty session ID")
	}

	writeACPRequest(t, inWriter, 4, acp.MethodSessionPrompt, acp.SessionPromptParams{
		SessionID: session.SessionID,
		Prompt: []acp.ContentBlock{{
			Type: "text",
			Text: "/llamacpp",
		}},
	})
	var prompt acp.SessionPromptResult
	readACPResponse(t, scanner, 4, &prompt)
	if prompt.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", prompt.StopReason)
	}

	writeACPRequest(t, inWriter, 5, acp.MethodFinalize, acp.FinalizeParams{Reason: "test complete"})
	var final acp.FinalizeResult
	readACPResponse(t, scanner, 5, &final)
	if final.ClosedSessions != 1 {
		t.Fatalf("closed sessions = %d, want 1", final.ClosedSessions)
	}

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after finalize")
	}
}

func TestCloseACPSessionWaitsForActivePromptCleanup(t *testing.T) {
	trace, err := core.OpenTrace(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	fakeSession := &fakeACPSession{}
	ctx, cancel := context.WithCancel(context.Background())
	session := &acpSession{
		comp: &RunComponents{
			Config:  &config.Config{Sandbox: config.SandboxConfig{Enabled: true}},
			Trace:   trace,
			Session: fakeSession,
		},
		cancel:       cancel,
		activeCtx:    ctx,
		promptActive: true,
	}

	go func() {
		<-ctx.Done()
		time.Sleep(25 * time.Millisecond)
		session.finishPrompt()
	}()

	closeACPSession(session)
	if !fakeSession.closed {
		t.Fatal("active session cleanup did not complete before close returned")
	}
}

func TestACPErrorsUseProtocolCodes(t *testing.T) {
	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
	}

	server.handleRequest(acpRequest(t, 1, acp.MethodSessionPrompt, acp.SessionPromptParams{SessionID: "missing"}))
	server.handleRequest(acpRequest(t, 2, acp.MethodSetSuggestedPrompts, acp.SetSuggestedPromptsParams{SessionID: "missing"}))

	responses := acpResponses(t, out.String())
	wantCodes := []int{acp.ErrSessionNotFound, acp.ErrSessionNotFound}
	if len(responses) != len(wantCodes) {
		t.Fatalf("responses = %#v", responses)
	}
	for i, want := range wantCodes {
		if responses[i].Error == nil || responses[i].Error.Code != want {
			t.Fatalf("response %d error = %#v, want code %d", i, responses[i].Error, want)
		}
	}
}

func acpRequest(t *testing.T, id int, method string, params any) acp.Request {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return acp.Request{JSONRPC: acp.JSONRPCVersion, Method: method, Params: raw, ID: id}
}

func writeACPRequest(t *testing.T, w io.Writer, id int, method string, params any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(acpRequest(t, id, method, params)); err != nil {
		t.Fatalf("write ACP request %s: %v", method, err)
	}
}

func readACPResponse(t *testing.T, scanner *bufio.Scanner, id int, dest any) {
	t.Helper()
	for scanner.Scan() {
		var res acp.Response
		if err := json.Unmarshal(scanner.Bytes(), &res); err == nil && res.ID == float64(id) {
			decodeACPResult(t, res, dest)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read ACP response %d: %v", id, err)
	}
	t.Fatalf("ACP response %d not found before stream ended", id)
}

func acpResponses(t *testing.T, raw string) []acp.Response {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]acp.Response, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var res acp.Response
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			t.Fatalf("response unmarshal %q: %v", line, err)
		}
		out = append(out, res)
	}
	return out
}

func decodeACPResult(t *testing.T, res acp.Response, dest any) {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("response error = %#v", res.Error)
	}
	if err := json.Unmarshal(res.Result, dest); err != nil {
		t.Fatalf("result unmarshal: %v", err)
	}
}

type fakeACPSession struct {
	closed bool
}

func (s *fakeACPSession) ID() string { return "fake" }

func (s *fakeACPSession) Type() string { return "host" }

func (s *fakeACPSession) Start(context.Context) error { return nil }

func (s *fakeACPSession) Close() error {
	s.closed = true
	return nil
}

func (s *fakeACPSession) Run(context.Context, executor.RunRequest) (executor.Result, error) {
	return executor.Result{}, nil
}

func (s *fakeACPSession) Workspace() string { return "" }

var _ executor.Session = (*fakeACPSession)(nil)

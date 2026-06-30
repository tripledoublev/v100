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
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
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
	if !initResult.AgentCapabilities.LoadSession ||
		initResult.AgentCapabilities.SessionCapabilities.Close == nil ||
		initResult.AgentCapabilities.SessionCapabilities.List == nil ||
		initResult.AgentCapabilities.SessionCapabilities.Resume == nil {
		t.Fatalf("initialize capabilities = %#v", initResult.AgentCapabilities)
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

	closeACPSession(session, "session_close")
	if !fakeSession.closed {
		t.Fatal("active session cleanup did not complete before close returned")
	}
}

func TestACPRunPromptEmitsRunLifecycleUpdates(t *testing.T) {
	var out bytes.Buffer
	server := &acpServer{
		conn:    acp.NewConn(strings.NewReader(""), &out),
		cfgPath: filepath.Join(t.TempDir(), "missing.toml"),
	}

	workspace := t.TempDir()
	trace, err := core.OpenTrace(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	run := &core.Run{ID: "acp-run-1", Dir: workspace, TraceFile: trace.Path()}
	prov := &acpLifecycleProvider{}
	reg := tools.NewRegistry(nil)
	pol := policy.Default()
	budget := core.NewBudgetTracker(&core.Budget{MaxSteps: 3, MaxTokens: 1000})
	loop := &core.Loop{
		Run:       run,
		Provider:  prov,
		Model:     "test-model",
		Tools:     reg,
		Policy:    pol,
		Trace:     trace,
		Budget:    budget,
		ConfirmFn: func(_, _ string) bool { return true },
		OutputFn:  acp.NewTranslator(server.conn, "session-1"),
		Mapper:    core.NewPathMapper(workspace, workspace),
	}
	session := &acpSession{
		comp: &RunComponents{
			Config:    config.DefaultConfig(),
			Run:       run,
			Provider:  prov,
			Registry:  reg,
			Policy:    pol,
			Trace:     trace,
			Budget:    budget,
			Workspace: workspace,
			Model:     "test-model",
		},
		loop: loop,
	}

	if stopReason := server.runPrompt(session, "session-1", "hello", nil); stopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", stopReason)
	}
	closeACPSession(session, "test complete")

	updates := acpSessionUpdates(t, out.String())
	var sawStart, sawEnd bool
	for _, update := range updates {
		if update.SessionID != "session-1" || update.Update.Type != "run_status_update" {
			continue
		}
		switch update.Update.Status {
		case "in_progress":
			var payload core.RunStartPayload
			if err := json.Unmarshal(update.Update.RawOutput, &payload); err != nil {
				t.Fatalf("start raw output: %v", err)
			}
			if payload.Provider == "acp-test" && payload.Model == "test-model" && payload.Workspace == workspace {
				sawStart = true
			}
		case "completed":
			var payload core.RunEndPayload
			if err := json.Unmarshal(update.Update.RawOutput, &payload); err != nil {
				t.Fatalf("end raw output: %v", err)
			}
			if payload.Reason == "test complete" {
				sawEnd = true
			}
		}
	}
	if !sawStart {
		t.Fatalf("missing run.start ACP update: %#v", updates)
	}
	if !sawEnd {
		t.Fatalf("missing run.end ACP update: %#v", updates)
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

func TestACPSessionNewAppliesProviderModelSolverOverrides(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.toml")
	writeACPSessionOverrideConfig(t, cfgPath)

	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
		cfgPath:  cfgPath,
		cmd:      &cobra.Command{},
	}
	server.handleRequest(acpRequest(t, 1, acp.MethodSessionNew, acp.SessionNewParams{
		SessionID: "override-session",
		CWD:       workspace,
		RunDir:    filepath.Join(root, "runs"),
		Provider:  "ollama",
		Model:     "chat-model",
		Solver:    "router",
	}))

	responses := acpResponses(t, out.String())
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	var result acp.SessionNewResult
	decodeACPResult(t, responses[0], &result)
	if result.SessionID != "override-session" {
		t.Fatalf("session ID = %q, want override-session", result.SessionID)
	}
	session := server.sessions["override-session"]
	if session == nil || session.loop == nil || session.comp == nil {
		t.Fatalf("session not registered correctly: %#v", server.sessions)
	}
	if got := session.loop.Provider.Name(); got != "ollama" {
		t.Fatalf("provider = %q, want ollama", got)
	}
	if session.loop.Model != "chat-model" || session.comp.Model != "chat-model" {
		t.Fatalf("model loop/comp = %q/%q, want chat-model", session.loop.Model, session.comp.Model)
	}
	if got := session.loop.Solver.Name(); got != "router" {
		t.Fatalf("solver = %q, want router", got)
	}

	closeACPSession(session, "session_close")
}

func TestACPSessionNewRejectsInvalidOverridesWithoutCreatingSession(t *testing.T) {
	tests := []struct {
		name   string
		params acp.SessionNewParams
	}{
		{
			name: "solver",
			params: acp.SessionNewParams{
				SessionID: "bad-solver-session",
				Solver:    "bogus",
			},
		},
		{
			name: "provider",
			params: acp.SessionNewParams{
				SessionID: "bad-provider-session",
				Provider:  "definitely-not-a-provider",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "workspace")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			cfgPath := filepath.Join(root, "config.toml")
			writeACPSessionOverrideConfig(t, cfgPath)

			var out bytes.Buffer
			server := &acpServer{
				conn:     acp.NewConn(strings.NewReader(""), &out),
				sessions: make(map[string]*acpSession),
				cfgPath:  cfgPath,
				cmd:      &cobra.Command{},
			}
			tt.params.CWD = workspace
			tt.params.RunDir = filepath.Join(root, "runs")
			server.handleRequest(acpRequest(t, 1, acp.MethodSessionNew, tt.params))

			responses := acpResponses(t, out.String())
			if len(responses) != 1 {
				t.Fatalf("responses = %#v", responses)
			}
			if responses[0].Error == nil || responses[0].Error.Code != acp.ErrInvalidSessionConfig {
				t.Fatalf("error = %#v, want invalid session config", responses[0].Error)
			}
			if _, ok := server.sessions[tt.params.SessionID]; ok {
				t.Fatalf("invalid session was registered: %#v", server.sessions[tt.params.SessionID])
			}
		})
	}
}

func TestACPSessionReconfigureSwitchesRuntimeAndPreservesHistory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.toml")
	writeACPSessionOverrideConfig(t, cfgPath)

	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
		cfgPath:  cfgPath,
		cmd:      &cobra.Command{},
	}
	server.handleRequest(acpRequest(t, 1, acp.MethodSessionNew, acp.SessionNewParams{
		SessionID: "reconfigure-session",
		CWD:       workspace,
		RunDir:    filepath.Join(root, "runs"),
	}))
	session := server.sessions["reconfigure-session"]
	if session == nil || session.loop == nil {
		t.Fatalf("session not registered: %#v", server.sessions)
	}
	session.loop.Messages = []providers.Message{{Role: "user", Content: "keep this"}}

	server.handleRequest(acpRequest(t, 2, acp.MethodSessionReconfigure, acp.SessionReconfigureParams{
		SessionID: "reconfigure-session",
		Provider:  "ollama",
		Model:     "chat-model",
		Solver:    "dual_channel",
	}))

	responses := acpResponses(t, out.String())
	if len(responses) != 2 {
		t.Fatalf("responses = %#v", responses)
	}
	var result acp.SessionReconfigureResult
	decodeACPResult(t, responses[1], &result)
	if result.Provider != "ollama" || result.Model != "chat-model" || result.Solver != "dual_channel" {
		t.Fatalf("reconfigure result = %#v", result)
	}
	if got := session.loop.Provider.Name(); got != "ollama" {
		t.Fatalf("provider = %q, want ollama", got)
	}
	if session.loop.Model != "chat-model" || session.comp.Model != "chat-model" {
		t.Fatalf("model loop/comp = %q/%q, want chat-model", session.loop.Model, session.comp.Model)
	}
	if got := session.loop.Solver.Name(); got != "dual_channel" {
		t.Fatalf("solver = %q, want dual_channel", got)
	}
	if len(session.loop.Messages) != 1 || session.loop.Messages[0].Content != "keep this" {
		t.Fatalf("history was not preserved: %#v", session.loop.Messages)
	}

	closeACPSession(session, "session_close")
}

func TestACPSessionNewProfileToolsFailClosed(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.toml")
	writeACPSessionOverrideConfig(t, cfgPath)

	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
		cfgPath:  cfgPath,
		cmd:      &cobra.Command{},
	}
	server.handleRequest(acpRequest(t, 1, acp.MethodSessionNew, acp.SessionNewParams{
		SessionID:    "profile-session",
		CWD:          workspace,
		RunDir:       filepath.Join(root, "runs"),
		Tools:        []string{"news_fetch", "sh"},
		Dangerous:    []string{},
		SystemPrompt: "profile prompt",
		NetworkTier:  "research",
		BudgetSteps:  7,
		BudgetTokens: 1234,
	}))

	responses := acpResponses(t, out.String())
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	var result acp.SessionNewResult
	decodeACPResult(t, responses[0], &result)
	session := server.sessions["profile-session"]
	if session == nil || session.loop == nil || session.comp == nil {
		t.Fatalf("session not registered correctly: %#v", server.sessions)
	}
	if names := session.comp.Registry.List(); len(names) != 1 || names[0] != "news_fetch" {
		t.Fatalf("registry tools = %v, want only [news_fetch]", names)
	}
	if _, ok := session.comp.Registry.Get("sh"); ok {
		t.Fatal("profile-scoped registry exposed sh")
	}
	for _, spec := range session.comp.Registry.Specs() {
		if spec.Name == "sh" {
			t.Fatalf("profile-scoped specs exposed sh: %#v", session.comp.Registry.Specs())
		}
	}
	if session.comp.Policy.SystemPrompt != "profile prompt" {
		t.Fatalf("policy prompt = %q, want profile prompt", session.comp.Policy.SystemPrompt)
	}
	if session.loop.NetworkTier != "research" {
		t.Fatalf("network tier = %q, want research", session.loop.NetworkTier)
	}
	if session.comp.Run.Budget.MaxSteps != 7 || session.comp.Run.Budget.MaxTokens != 1234 {
		t.Fatalf("budget = %+v", session.comp.Run.Budget)
	}

	closeACPSession(session, "session_close")
}

func TestACPListSessionsIncludesActiveAndRestorableRuns(t *testing.T) {
	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	restorableID := "20260613T010101-deadbeef"
	writeACPTraceFixture(t, runRoot, restorableID, workspace, "completed")

	activeID := "20260613T020202-feedbeef"
	activeDir := writeACPTraceFixture(t, runRoot, activeID, workspace, "")
	trace, err := core.OpenTrace(filepath.Join(activeDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
	}
	server.sessions[activeID] = &acpSession{
		comp: &RunComponents{
			Run:       &core.Run{ID: activeID, Dir: workspace, TraceFile: filepath.Join(activeDir, "trace.jsonl")},
			Trace:     trace,
			Workspace: workspace,
			Model:     "test-model",
		},
		promptActive: true,
	}

	server.handleRequest(acpRequest(t, 1, acp.MethodSessionList, acp.SessionListParams{RunDir: runRoot}))
	responses := acpResponses(t, out.String())
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	var result acp.SessionListResult
	decodeACPResult(t, responses[0], &result)
	if len(result.Sessions) != 2 {
		t.Fatalf("sessions = %#v, want active + restorable", result.Sessions)
	}
	byRunID := map[string]acp.SessionInfo{}
	for _, session := range result.Sessions {
		byRunID[session.RunID] = session
	}
	active := byRunID[activeID]
	if !active.Active || active.State != "busy" || !active.Restorable {
		t.Fatalf("active session info = %#v", active)
	}
	restorable := byRunID[restorableID]
	if restorable.Active || !restorable.Restorable || restorable.State != "ended" || restorable.EndReason != "completed" {
		t.Fatalf("restorable session info = %#v", restorable)
	}
	if restorable.Provider != "llamacpp" || restorable.Model != "test-model" || restorable.Workspace != workspace {
		t.Fatalf("restorable metadata = %#v", restorable)
	}
}

func TestACPResumeLoadsPriorTraceAsSession(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[providers.llamacpp]
type = "llamacpp"
default_model = "test-model"
base_url = "http://127.0.0.1:19091/v1"

[defaults]
provider = "llamacpp"
cheap_provider = "llamacpp"
smart_provider = "llamacpp"
compress_provider = "llamacpp"
confirm_tools = "dangerous"
budget_steps = 2
budget_tokens = 1000
max_tool_calls_per_step = 1
`), 0o644); err != nil {
		t.Fatal(err)
	}

	runID := "20260613T030303-cafebabe"
	writeACPTraceFixture(t, filepath.Join(root, "runs"), runID, workspace, "prompt_exit")

	var out bytes.Buffer
	server := &acpServer{
		conn:     acp.NewConn(strings.NewReader(""), &out),
		sessions: make(map[string]*acpSession),
		cfgPath:  cfgPath,
		cmd:      &cobra.Command{},
	}
	server.handleRequest(acpRequest(t, 1, acp.MethodSessionResume, acp.SessionResumeParams{RunID: runID}))
	responses := acpResponses(t, out.String())
	if len(responses) == 0 {
		t.Fatal("no ACP response")
	}
	var result acp.SessionResumeResult
	decodeACPResult(t, responses[0], &result)
	if result.SessionID != runID || result.RunID != runID {
		t.Fatalf("resume result = %#v", result)
	}
	session := server.sessions[runID]
	if session == nil || session.loop == nil {
		t.Fatalf("session not registered: %#v", server.sessions)
	}
	if got := len(session.loop.Messages); got < 3 {
		t.Fatalf("resumed messages = %d, want summary + prior user/assistant", got)
	}
	if !strings.Contains(session.loop.Messages[0].Content, "Resume summary for run "+runID) {
		t.Fatalf("first resumed message = %#v", session.loop.Messages[0])
	}
	if session.loop.Messages[1].Role != "user" || session.loop.Messages[2].Role != "assistant" {
		t.Fatalf("resumed history = %#v", session.loop.Messages[:3])
	}

	closeACPSession(session, "session_close")
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

func writeACPSessionOverrideConfig(t *testing.T, cfgPath string) {
	t.Helper()
	if err := os.WriteFile(cfgPath, []byte(`
[providers.llamacpp]
type = "llamacpp"
default_model = "test-model"
base_url = "http://127.0.0.1:19091/v1"

[providers.ollama]
type = "ollama"
default_model = "ollama-default"
base_url = "http://127.0.0.1:11434"

[embedding]
provider = "llamacpp"
model = "test-model"

[defaults]
provider = "llamacpp"
cheap_provider = "llamacpp"
smart_provider = "ollama"
compress_provider = "llamacpp"
confirm_tools = "dangerous"
budget_steps = 1
budget_tokens = 1000
max_tool_calls_per_step = 1
solver = "react"
`), 0o644); err != nil {
		t.Fatal(err)
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
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("response envelope unmarshal %q: %v", line, err)
		}
		if len(envelope.ID) == 0 {
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

func acpSessionUpdates(t *testing.T, raw string) []acp.SessionUpdateParams {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]acp.SessionUpdateParams, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var n acp.Notification
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			t.Fatalf("notification unmarshal %q: %v", line, err)
		}
		if n.Method != acp.MethodSessionUpdate {
			continue
		}
		var params acp.SessionUpdateParams
		if err := json.Unmarshal(n.Params, &params); err != nil {
			t.Fatalf("session update unmarshal: %v", err)
		}
		out = append(out, params)
	}
	return out
}

type acpLifecycleProvider struct{}

func (p *acpLifecycleProvider) Name() string { return "acp-test" }

func (p *acpLifecycleProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}

func (p *acpLifecycleProvider) Complete(context.Context, providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{AssistantText: "done"}, nil
}

func (p *acpLifecycleProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}

func (p *acpLifecycleProvider) Metadata(_ context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: model, ContextSize: 4096}, nil
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

func writeACPTraceFixture(t *testing.T, runRoot, runID, workspace, endReason string) string {
	t.Helper()
	runDir := filepath.Join(runRoot, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteMeta(runDir, core.RunMeta{
		RunID:           runID,
		Provider:        "llamacpp",
		Model:           "test-model",
		SourceWorkspace: workspace,
		CreatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()
	writeACPTraceEvent(t, trace, runID, "start", core.EventRunStart, core.RunStartPayload{
		Provider:  "llamacpp",
		Model:     "test-model",
		Workspace: workspace,
	})
	writeACPTraceEvent(t, trace, runID, "user", core.EventUserMsg, core.UserMsgPayload{Content: "prior prompt"})
	writeACPTraceEvent(t, trace, runID, "assistant", core.EventModelResp, core.ModelRespPayload{Text: "prior answer"})
	if strings.TrimSpace(endReason) != "" {
		writeACPTraceEvent(t, trace, runID, "end", core.EventRunEnd, core.RunEndPayload{Reason: endReason})
	}
	return runDir
}

func writeACPTraceEvent(t *testing.T, trace *core.TraceWriter, runID, eventID string, eventType core.EventType, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   runID,
		EventID: eventID,
		Type:    eventType,
		Payload: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func (s *fakeACPSession) Run(context.Context, executor.RunRequest) (executor.Result, error) {
	return executor.Result{}, nil
}

func (s *fakeACPSession) Workspace() string { return "" }

var _ executor.Session = (*fakeACPSession)(nil)

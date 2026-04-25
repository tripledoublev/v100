package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type testProviderStub struct {
	requests []providers.CompleteRequest
	caps     providers.Capabilities
}

func (p *testProviderStub) Name() string { return "test-stub" }
func (p *testProviderStub) Capabilities() providers.Capabilities {
	return p.caps
}
func (p *testProviderStub) Complete(_ context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	p.requests = append(p.requests, req)
	return providers.CompleteResponse{AssistantText: "ok"}, nil
}
func (p *testProviderStub) Embed(_ context.Context, _ providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (p *testProviderStub) Metadata(_ context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: model, ContextSize: 4096}, nil
}

func testInteractiveConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Providers["smartrouter"] = config.ProviderConfig{Type: "smartrouter"}
	cfg.Providers["ollama"] = config.ProviderConfig{
		Type:         "ollama",
		DefaultModel: "qwen-local",
		BaseURL:      "http://127.0.0.1:11434",
	}
	cfg.Providers["codex"] = config.ProviderConfig{
		Type:         "ollama",
		DefaultModel: "gpt-frontier",
		BaseURL:      "http://127.0.0.1:11435",
	}
	cfg.Providers["gemini"] = config.ProviderConfig{
		Type:         "llamacpp",
		DefaultModel: "gemini-frontier",
		BaseURL:      "http://127.0.0.1:19091/v1",
	}
	cfg.Providers["minimax"] = config.ProviderConfig{
		Type:         "ollama",
		DefaultModel: "minimax-frontier",
		BaseURL:      "http://127.0.0.1:11436",
	}
	cfg.Defaults.Provider = "smartrouter"
	cfg.Defaults.CheapProvider = "ollama"
	cfg.Defaults.SmartProvider = "codex"
	return cfg
}

func newInteractiveTestLoop(t *testing.T) *core.Loop {
	t.Helper()
	runDir := t.TempDir()
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = trace.Close() })
	return &core.Loop{
		Run:      &core.Run{ID: "interactive-test", Dir: runDir},
		Provider: &testProviderStub{},
		Tools:    tools.NewRegistry(nil),
		Trace:    trace,
		Budget:   core.NewBudgetTracker(&core.Budget{MaxSteps: 10, MaxTokens: 10000}),
		Solver:   &core.ReactSolver{},
	}
}

func TestParseInteractiveModeCommand(t *testing.T) {
	mode, rest, ok := parseInteractiveModeCommand("/codex inspect this")
	if !ok || mode != "/codex" || rest != "inspect this" {
		t.Fatalf("parseInteractiveModeCommand returned (%q, %q, %v)", mode, rest, ok)
	}

	mode, rest, ok = parseInteractiveModeCommand("plain text")
	if ok || mode != "" || rest != "plain text" {
		t.Fatalf("unexpected parse result for plain text: (%q, %q, %v)", mode, rest, ok)
	}

	mode, rest, ok = parseInteractiveModeCommand("/claude hello")
	if !ok || mode != "/claude" || rest != "hello" {
		t.Fatalf("parseInteractiveModeCommand(/claude) returned (%q, %q, %v)", mode, rest, ok)
	}

	mode, rest, ok = parseInteractiveModeCommand("/anthropic hi")
	if !ok || mode != "/anthropic" || rest != "hi" {
		t.Fatalf("parseInteractiveModeCommand(/anthropic) returned (%q, %q, %v)", mode, rest, ok)
	}
}

func TestBuildProviderSupportsSmartRouterType(t *testing.T) {
	cfg := testInteractiveConfig()
	prov, err := buildProvider(cfg, "smartrouter")
	if err != nil {
		t.Fatalf("buildProvider(smartrouter) error = %v", err)
	}
	if prov.Name() != "smartrouter" {
		t.Fatalf("provider name = %q, want smartrouter", prov.Name())
	}
}

func TestBuildSolverAutoUsesSmartRouterWhenProviderDefaultIsSmartRouter(t *testing.T) {
	cfg := testInteractiveConfig()
	solver, err := buildSolver(cfg, "")
	if err != nil {
		t.Fatalf("buildSolver error = %v", err)
	}
	if _, ok := solver.(*core.RouterSolver); !ok {
		t.Fatalf("solver type = %T, want *core.RouterSolver", solver)
	}
}

func TestBuildSolverRouterUsesConfiguredCheapProvider(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	cfg := testInteractiveConfig()
	cfg.Providers["glm"] = config.ProviderConfig{
		Type:         "glm",
		DefaultModel: "GLM-5.1",
		BaseURL:      "https://api.z.ai/api/coding/paas/v4",
		Auth:         config.AuthConfig{Env: "ZHIPU_API_KEY"},
	}
	cfg.Defaults.CheapProvider = "glm"
	cfg.Defaults.SmartProvider = "codex"

	solver, err := buildSolver(cfg, "router")
	if err != nil {
		t.Fatalf("buildSolver error = %v", err)
	}
	router, ok := solver.(*core.RouterSolver)
	if !ok {
		t.Fatalf("solver type = %T, want *core.RouterSolver", solver)
	}
	if router.Cheap.Name() != "glm" {
		t.Fatalf("cheap provider = %q, want glm", router.Cheap.Name())
	}
}

func TestBuildCompressProviderUsesMainProviderForCloudDefault(t *testing.T) {
	cfg := testInteractiveConfig()
	cfg.Defaults.Provider = "glm"
	cfg.Defaults.CheapProvider = "ollama"
	cfg.Defaults.CompressProvider = ""
	cp := buildCompressProvider(cfg)
	if cp != nil {
		t.Fatalf("compress provider = %q, want nil to reuse main provider", cp.Name())
	}
}

func TestBuildCompressProviderPrefersLocalProviderForLocalDefault(t *testing.T) {
	cfg := testInteractiveConfig()
	cfg.Defaults.Provider = "ollama"
	cfg.Defaults.CheapProvider = "gemini"
	cfg.Defaults.CompressProvider = ""
	cp := buildCompressProvider(cfg)
	if cp == nil {
		t.Fatal("expected non-nil compress provider")
	}
	if cp.Name() != "ollama" && cp.Name() != "llamacpp" {
		t.Fatalf("compress provider = %q, want a local backend", cp.Name())
	}
}

func TestApplyInteractiveModeSwitchesToAuto(t *testing.T) {
	cfg := testInteractiveConfig()
	loop := newInteractiveTestLoop(t)
	rewritten, handled, err := applyInteractiveMode(context.Background(), cfg, loop, "/auto inspect the project", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if handled {
		t.Fatal("expected trailing prompt to continue after mode switch")
	}
	if rewritten != "inspect the project" {
		t.Fatalf("rewritten input = %q", rewritten)
	}
	if loop.Provider.Name() != "smartrouter" {
		t.Fatalf("provider name = %q, want smartrouter", loop.Provider.Name())
	}
	if _, ok := loop.Solver.(*core.RouterSolver); !ok {
		t.Fatalf("solver type = %T, want *core.RouterSolver", loop.Solver)
	}
}

func TestApplyInteractiveModeHandlesStandaloneSwitch(t *testing.T) {
	cfg := testInteractiveConfig()
	loop := newInteractiveTestLoop(t)
	_, handled, err := applyInteractiveMode(context.Background(), cfg, loop, "/local", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if !handled {
		t.Fatal("expected standalone mode switch to be fully handled")
	}
	if loop.Provider.Name() != "ollama" {
		t.Fatalf("provider name = %q, want ollama", loop.Provider.Name())
	}
	if _, ok := loop.Solver.(*core.ReactSolver); !ok {
		t.Fatalf("solver type = %T, want *core.ReactSolver", loop.Solver)
	}
}

func TestRunCLIInputTreatsModeSwitchAsNonModelTurn(t *testing.T) {
	cfg := testInteractiveConfig()
	runDir := t.TempDir()
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	mock := &testProviderStub{}
	loop := &core.Loop{
		Run:      &core.Run{ID: "cli-input", Dir: runDir},
		Provider: mock,
		Tools:    tools.NewRegistry(nil),
		Trace:    trace,
		Budget:   core.NewBudgetTracker(&core.Budget{MaxSteps: 10, MaxTokens: 10000}),
		Solver:   &core.ReactSolver{},
	}
	if err := runCLIInput(context.Background(), cfg, loop, "/local", nil, false); err != nil {
		t.Fatalf("runCLIInput error = %v", err)
	}
	if len(mock.requests) != 0 {
		t.Fatalf("expected no model request for standalone mode switch, got %d", len(mock.requests))
	}
}

func TestApplyInteractiveModeLeavesPlanModeAlone(t *testing.T) {
	cfg := testInteractiveConfig()
	loop := newInteractiveTestLoop(t)
	rewritten, handled, err := applyInteractiveMode(context.Background(), cfg, loop, "/auto inspect the project", true)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if handled || rewritten != "/auto inspect the project" {
		t.Fatalf("plan mode should leave input untouched, got (%q, %v)", rewritten, handled)
	}
}

func TestEmitSessionNoticeWritesTrace(t *testing.T) {
	loop := newInteractiveTestLoop(t)
	emitSessionNotice(loop, "session mode switched to auto")
	events, err := core.ReadAll(loop.Trace.Path())
	if err != nil {
		t.Fatalf("ReadAll error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Type != core.EventUserMsg {
		t.Fatalf("event type = %q, want %q", events[0].Type, core.EventUserMsg)
	}
}

func TestBuildSingleProviderSelectionUsesConfiguredModel(t *testing.T) {
	cfg := testInteractiveConfig()
	sel, err := buildSingleProviderSelection(cfg, "codex", "codex")
	if err != nil {
		t.Fatalf("buildSingleProviderSelection error = %v", err)
	}
	if sel.Model != "gpt-frontier" {
		t.Fatalf("selection model = %q, want gpt-frontier", sel.Model)
	}
	if sel.Provider == nil || sel.Provider.Name() != "ollama" {
		t.Fatalf("selection provider = %v, want ollama-backed provider", sel.Provider)
	}
}

func TestSmartRouterProviderCapabilitiesUnion(t *testing.T) {
	prov := &providers.SmartRouterProvider{
		Cheap: &testProviderStub{caps: providers.Capabilities{ToolCalls: true}},
		Smart: &testProviderStub{caps: providers.Capabilities{Images: true, Streaming: true}},
	}
	caps := prov.Capabilities()
	if !caps.ToolCalls || !caps.Images || !caps.Streaming {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestApplyInteractiveModeModelSelection(t *testing.T) {
	cfg := testInteractiveConfig()
	loop := newInteractiveTestLoop(t)

	// 1. List models for a specific provider (by name)
	_, handled, err := applyInteractiveMode(context.Background(), cfg, loop, "/model gemini", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for model list")
	}

	// 2. Select a provider and model (by name)
	_, handled, err = applyInteractiveMode(context.Background(), cfg, loop, "/model gemini gemini-frontier", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for model selection")
	}
	if loop.Provider.Name() != "llamacpp" { // gemini is llamacpp in test config
		t.Fatalf("provider name = %q, want llamacpp", loop.Provider.Name())
	}
	if loop.Model != "gemini-frontier" {
		t.Fatalf("loop.Model = %q, want gemini-frontier", loop.Model)
	}

	// 3. Select a provider and model (by number)
	// getSortedProviders: anthropic, claude, codex, gemini, glm, minimax, mistral, ollama, openai
	// gemini is 4th
	_, handled, err = applyInteractiveMode(context.Background(), cfg, loop, "/model 4", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true for provider selection by number")
	}
}

func TestApplyInteractiveModeModelWithTask(t *testing.T) {
	cfg := testInteractiveConfig()
	loop := newInteractiveTestLoop(t)

	// Select provider, model and provide a task
	rewritten, handled, err := applyInteractiveMode(context.Background(), cfg, loop, "/model gemini gemini-frontier list files", false)
	if err != nil {
		t.Fatalf("applyInteractiveMode error = %v", err)
	}
	if handled {
		t.Fatal("expected handled=false because there is a trailing task")
	}
	if rewritten != "list files" {
		t.Fatalf("rewritten = %q, want 'list files'", rewritten)
	}
}

func TestModelPassedToProvider(t *testing.T) {
	loop := newInteractiveTestLoop(t)
	mock := &testProviderStub{}
	loop.Provider = mock
	loop.Model = "custom-model"

	// Trigger a model call through solver (Step)
	// We'll use Step which calls solver.Solve
	ctx := context.Background()
	if err := loop.Step(ctx, "hello"); err != nil {
		t.Fatalf("Step error = %v", err)
	}

	if len(mock.requests) == 0 {
		t.Fatal("expected at least one request to provider")
	}
	// The solver should have passed loop.Model to the provider
	found := false
	for _, req := range mock.requests {
		if req.Model == "custom-model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("custom-model not found in provider requests: %+v", mock.requests)
	}

	// Now test switching via /model and then stepping
	cfg := testInteractiveConfig()
	mock2 := &testProviderStub{}
	loop.Provider = mock2
	// /model 4 -> gemini (which is llamacpp in test config)
	_, handled, err := applyInteractiveMode(ctx, cfg, loop, "/model 4 gemini-frontier", false)
	if err != nil || !handled {
		t.Fatalf("applyInteractiveMode error = %v, handled = %v", err, handled)
	}
	if loop.Model != "gemini-frontier" {
		t.Fatalf("loop.Model = %q, want gemini-frontier", loop.Model)
	}

	// We need to swap the provider in the loop because applyInteractiveMode
	// builds a new provider instance. For testing we want our mock.
	loop.Provider = mock2

	if err := loop.Step(ctx, "hello again"); err != nil {
		t.Fatalf("Step error = %v", err)
	}
	if len(mock2.requests) == 0 {
		t.Fatal("expected requests to second provider")
	}
	if mock2.requests[0].Model != "gemini-frontier" {
		t.Fatalf("request model = %q, want gemini-frontier", mock2.requests[0].Model)
	}
}

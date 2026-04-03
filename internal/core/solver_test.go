package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockDangerousDeniedTool struct{}

func (t *mockDangerousDeniedTool) Name() string        { return "mock_dangerous" }
func (t *mockDangerousDeniedTool) Description() string { return "dangerous test tool" }
func (t *mockDangerousDeniedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *mockDangerousDeniedTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *mockDangerousDeniedTool) DangerLevel() tools.DangerLevel { return tools.Dangerous }
func (t *mockDangerousDeniedTool) Effects() tools.ToolEffects     { return tools.ToolEffects{} }
func (t *mockDangerousDeniedTool) Exec(ctx context.Context, call tools.ToolCallContext, args json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{OK: true, Output: "unexpected"}, nil
}

// MockProvider for testing solvers
type MockProvider struct {
	Responses []providers.CompleteResponse
	Requests  []providers.CompleteRequest
	Caps      providers.Capabilities
	idx       int
}

func (p *MockProvider) Name() string { return "mock" }
func (p *MockProvider) Capabilities() providers.Capabilities {
	if p.Caps == (providers.Capabilities{}) {
		return providers.Capabilities{ToolCalls: true}
	}
	return p.Caps
}
func (p *MockProvider) Complete(ctx context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	p.Requests = append(p.Requests, req)
	if p.idx >= len(p.Responses) {
		return providers.CompleteResponse{AssistantText: "done"}, nil
	}
	res := p.Responses[p.idx]
	p.idx++
	return res, nil
}
func (p *MockProvider) Embed(ctx context.Context, req providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{Embedding: []float32{0.1, 0.2}}, nil
}
func (p *MockProvider) Metadata(ctx context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: "mock", ContextSize: 4096}, nil
}

func TestReactSolver(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "I will read a file.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "fs_read", Args: json.RawMessage(`{"path":"test.txt"}`)},
				},
			},
			{
				AssistantText: "File read successful. Task complete.",
			},
		},
	}

	reg := tools.NewRegistry([]string{"fs_read"})
	reg.Register(tools.FSRead())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Mapper:   NewPathMapper(runDir, runDir),
	}

	res, err := l.Solver.Solve(ctx, l, "Read test.txt")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}

	if res.Steps != 1 {
		t.Errorf("Expected 1 solver step, got %d", res.Steps)
	}
	if !contains(res.FinalText, "Task complete") {
		t.Errorf("Final text mismatch: %s", res.FinalText)
	}
}

func TestReactSolverThresholdHookIgnoresToolFreeTurn(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{AssistantText: "Answer without tools."},
		},
	}

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    tools.NewRegistry(nil),
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Mapper:   NewPathMapper(runDir, runDir),
		Hooks:    []PolicyHook{ThresholdHook(1)},
	}

	res, err := l.Solver.Solve(ctx, l, "Say hi")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "without tools") {
		t.Fatalf("unexpected final text: %q", res.FinalText)
	}
}

func TestReactSolverDeduplicationHookWarnsBeforeThirdRequest(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "List the workspace.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				},
			},
			{
				AssistantText: "List it again.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_2", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				},
			},
			{
				AssistantText: "I already have the listing.",
			},
		},
	}

	reg := tools.NewRegistry([]string{"fs_list"})
	reg.Register(tools.FSList())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Mapper:   NewPathMapper(runDir, runDir),
		Hooks:    []PolicyHook{DeduplicationHook(2)},
	}

	res, err := l.Solver.Solve(ctx, l, "inspect")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "already have the listing") {
		t.Fatalf("unexpected final text: %q", res.FinalText)
	}
	if len(p.Requests) != 3 {
		t.Fatalf("provider requests = %d, want 3", len(p.Requests))
	}

	foundWarning := false
	for _, msg := range p.Requests[2].Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "DEDUPLICATION WARNING") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected dedup warning in third request, got %+v", p.Requests[2].Messages)
	}
}

func TestReactSolverModelCallTracksImageAudit(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Caps:      providers.Capabilities{ToolCalls: true, Images: true},
		Responses: []providers.CompleteResponse{{AssistantText: "done"}},
	}
	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()
	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    tools.NewRegistry(nil),
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Policy:   policy.Default(),
		Mapper:   NewPathMapper(runDir, runDir),
	}
	err := l.StepWithImages(ctx, "what do you see", []providers.ImageAttachment{{MIMEType: "image/png", Data: []byte("fake")}})
	if err != nil {
		t.Fatalf("StepWithImages failed: %v", err)
	}
	events, err := ReadAll(trace.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type != EventModelCall {
			continue
		}
		var payload ModelCallPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ImageCount != 1 {
			t.Fatalf("image_count = %d, want 1", payload.ImageCount)
		}
		if !payload.ProviderSupportsImage {
			t.Fatal("expected provider_supports_image=true")
		}
		if len(payload.MessageImageCounts) != len(payload.Messages) {
			t.Fatalf("message_image_counts length = %d, want %d", len(payload.MessageImageCounts), len(payload.Messages))
		}
		if payload.MessageImageCounts[len(payload.MessageImageCounts)-1] != 1 {
			t.Fatalf("message_image_counts = %v, want trailing 1", payload.MessageImageCounts)
		}
		return
	}
	t.Fatal("expected model.call event")
}

func TestPlanExecuteSolver(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{AssistantText: "1. Read file\n2. Done."}, // Plan
			{AssistantText: "Executing step 1...", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "fs_read", Args: json.RawMessage(`{"path":"test.txt"}`)},
			}},
			{AssistantText: "Task complete."},
		},
	}

	reg := tools.NewRegistry([]string{"fs_read"})
	reg.Register(tools.FSRead())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &PlanExecuteSolver{MaxReplans: 1},
		Mapper:   NewPathMapper(runDir, runDir),
	}

	res, err := l.Solver.Solve(ctx, l, "Read test.txt with a plan")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}

	if !contains(res.FinalText, "Task complete") {
		t.Errorf("Final text mismatch: %s", res.FinalText)
	}
}

func TestCheckpoint(t *testing.T) {
	l := &Loop{
		Messages: []providers.Message{
			{Role: "user", Content: "hello"},
		},
		stepCount: 5,
	}

	cp := l.Checkpoint()
	l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: "hi"})
	l.stepCount = 6

	l.Restore(cp)

	if len(l.Messages) != 1 {
		t.Errorf("Expected 1 message after restore, got %d", len(l.Messages))
	}
	if l.stepCount != 5 {
		t.Errorf("Expected step count 5 after restore, got %d", l.stepCount)
	}
}

func TestReactSolverDeniedToolInjectsSystemMessage(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "mock_dangerous", Args: json.RawMessage(`{}`)},
				},
			},
			{
				AssistantText: "I need approval before I can do that.",
			},
		},
	}

	reg := tools.NewRegistry([]string{"mock_dangerous"})
	reg.Register(&mockDangerousDeniedTool{})

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:       &Run{ID: "test-run", Dir: runDir},
		Provider:  p,
		Tools:     reg,
		Trace:     trace,
		Budget:    NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:    &ReactSolver{},
		ConfirmFn: func(_, _ string) bool { return false },
		Mapper:    NewPathMapper(runDir, runDir),
	}

	res, err := l.Solver.Solve(ctx, l, "use the dangerous tool")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "approval") {
		t.Fatalf("expected final text to reflect denial, got %q", res.FinalText)
	}

	foundSystemDenial := false
	for _, msg := range l.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, `tool "mock_dangerous" was denied`) {
			foundSystemDenial = true
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `tool "mock_dangerous" was denied`) {
			t.Fatalf("denial steering should not be recorded as a user message: %+v", msg)
		}
	}
	if !foundSystemDenial {
		t.Fatal("expected denial steering to be recorded as a system message")
	}
}

func TestReactSolverRepeatedDeniedToolStopsRetryLoop(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "mock_dangerous", Args: json.RawMessage(`{}`)},
				},
			},
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call_2", Name: "mock_dangerous", Args: json.RawMessage(`{}`)},
				},
			},
			{
				AssistantText: "I need approval before I can continue.",
			},
		},
	}

	reg := tools.NewRegistry([]string{"mock_dangerous"})
	reg.Register(&mockDangerousDeniedTool{})

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:       &Run{ID: "test-run", Dir: runDir},
		Provider:  p,
		Tools:     reg,
		Trace:     trace,
		Budget:    NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:    &ReactSolver{},
		ConfirmFn: func(_, _ string) bool { return false },
		Mapper:    NewPathMapper(runDir, runDir),
	}

	res, err := l.Solver.Solve(ctx, l, "use the dangerous tool")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "approval") {
		t.Fatalf("expected final text to reflect denial, got %q", res.FinalText)
	}
	if p.idx != len(p.Responses) {
		t.Fatalf("expected all scripted provider responses to be consumed, used=%d total=%d", p.idx, len(p.Responses))
	}

	toolResults := 0
	foundRepeatedDenialStop := false
	for _, msg := range l.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "user denied tool execution") {
			toolResults++
		}
		if msg.Role == "system" && strings.Contains(msg.Content, `was denied 2 times with the same arguments`) {
			foundRepeatedDenialStop = true
		}
	}
	if toolResults != 2 {
		t.Fatalf("expected exactly 2 denied tool results before stop, got %d", toolResults)
	}
	if !foundRepeatedDenialStop {
		t.Fatal("expected repeated-denial stop message to be recorded as a system message")
	}
}

func TestReactSolverWatchdogStopsToolsButAllowsFinalSynthesisTurn(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "call-1", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-2", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-3", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{ToolCalls: []providers.ToolCall{
				{ID: "call-4", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-5", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-6", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{ToolCalls: []providers.ToolCall{
				{ID: "call-7", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-8", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{AssistantText: "Synthesis after watchdog."},
		},
	}

	reg := tools.NewRegistry([]string{"fs_list"})
	reg.Register(tools.FSList())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Mapper:   NewPathMapper(runDir, runDir),
	}

	res, err := l.Solver.Solve(ctx, l, "inspect")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "Synthesis after watchdog") {
		t.Fatalf("unexpected final text: %q", res.FinalText)
	}
	if len(p.Requests) < 4 {
		t.Fatalf("expected at least 4 provider requests, got %d", len(p.Requests))
	}
	lastReq := p.Requests[len(p.Requests)-1]
	if len(lastReq.Tools) != 0 {
		t.Fatalf("expected watchdog synthesis turn to disable tools, got %d tools", len(lastReq.Tools))
	}
	foundWatchdogMessage := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Tool use is now DISABLED for the remainder of this step") {
			foundWatchdogMessage = true
			break
		}
	}
	if !foundWatchdogMessage {
		t.Fatalf("expected watchdog system message in final synthesis request, got %+v", lastReq.Messages)
	}
}

func TestReactSolverCanDisableWatchdogForAutonomousRuns(t *testing.T) {
	ctx := context.Background()
	p := &MockProvider{
		Responses: []providers.CompleteResponse{
			{ToolCalls: []providers.ToolCall{
				{ID: "call-1", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-2", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-3", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{ToolCalls: []providers.ToolCall{
				{ID: "call-4", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-5", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-6", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{ToolCalls: []providers.ToolCall{
				{ID: "call-7", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
				{ID: "call-8", Name: "fs_list", Args: json.RawMessage(`{"path":"."}`)},
			}},
			{AssistantText: "Continued synthesis without watchdog interruption."},
		},
	}

	reg := tools.NewRegistry([]string{"fs_list"})
	reg.Register(tools.FSList())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Mapper:   NewPathMapper(runDir, runDir),
		Policy:   &policy.Policy{DisableWatchdogs: true},
	}

	res, err := l.Solver.Solve(ctx, l, "inspect")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !contains(res.FinalText, "Continued synthesis without watchdog interruption") {
		t.Fatalf("unexpected final text: %q", res.FinalText)
	}
	if len(p.Requests) != 4 {
		t.Fatalf("expected 4 provider calls, got %d", len(p.Requests))
	}
	lastReq := p.Requests[len(p.Requests)-1]
	if len(lastReq.Tools) == 0 {
		t.Fatalf("expected final request to keep tools available when watchdogs are disabled")
	}
	for _, msg := range lastReq.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "System watchdog:") {
			t.Fatalf("did not expect watchdog message when disabled, got %+v", lastReq.Messages)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

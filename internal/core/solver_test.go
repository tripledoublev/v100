package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
	idx       int
}

func (p *MockProvider) Name() string { return "mock" }
func (p *MockProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true}
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

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

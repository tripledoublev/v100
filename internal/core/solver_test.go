package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

// MockProvider for testing solvers
type MockProvider struct {
	Responses []providers.CompleteResponse
	idx       int
}

func (p *MockProvider) Name() string { return "mock" }
func (p *MockProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true}
}
func (p *MockProvider) Complete(ctx context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
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
	defer trace.Close()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
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
	defer trace.Close()

	l := &Loop{
		Run:      &Run{ID: "test-run", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &PlanExecuteSolver{MaxReplans: 1},
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

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

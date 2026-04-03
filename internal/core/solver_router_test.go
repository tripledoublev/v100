package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

func newRouterTestLoop(t *testing.T, cheap, smart providers.Provider, enabledTools []string) *Loop {
	t.Helper()

	runDir := t.TempDir()
	trace, err := OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = trace.Close() })

	reg := tools.NewRegistry(enabledTools)
	for _, name := range enabledTools {
		switch name {
		case "fs_mkdir":
			reg.Register(tools.FSMkdir())
		case "fs_write":
			reg.Register(tools.FSWrite())
		}
	}

	return &Loop{
		Run:      &Run{ID: "router-test", Dir: runDir},
		Provider: cheap,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10, MaxTokens: 100000}),
		Solver:   &RouterSolver{Cheap: cheap, Smart: smart},
		Mapper:   NewPathMapper(runDir, runDir),
	}
}

func TestRouterSolverKeepsFSMkdirOnCheapTier(t *testing.T) {
	ctx := context.Background()
	cheap := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "Create the directory.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "fs_mkdir", Args: json.RawMessage(`{"path":"subdir"}`)},
				},
			},
			{AssistantText: "Done."},
		},
	}
	smart := &MockProvider{}
	loop := newRouterTestLoop(t, cheap, smart, []string{"fs_mkdir"})

	res, err := loop.Solver.Solve(ctx, loop, "Create subdir")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if res.FinalText != "Done." {
		t.Fatalf("FinalText = %q, want Done.", res.FinalText)
	}
	if len(cheap.Requests) != 2 {
		t.Fatalf("cheap requests = %d, want 2", len(cheap.Requests))
	}
	if len(smart.Requests) != 0 {
		t.Fatalf("smart requests = %d, want 0", len(smart.Requests))
	}
	if _, err := os.Stat(filepath.Join(loop.Run.Dir, "subdir")); err != nil {
		t.Fatalf("expected fs_mkdir to run on cheap tier: %v", err)
	}
}

func TestRouterSolverEscalatesUnknownCheapTierTool(t *testing.T) {
	ctx := context.Background()
	cheap := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "Use a made-up tool.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "title", Args: json.RawMessage(`{}`)},
				},
			},
		},
	}
	smart := &MockProvider{
		Responses: []providers.CompleteResponse{
			{AssistantText: "Recovered on smart tier."},
		},
	}
	loop := newRouterTestLoop(t, cheap, smart, nil)

	res, err := loop.Solver.Solve(ctx, loop, "Handle the issue")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if res.FinalText != "Recovered on smart tier." {
		t.Fatalf("FinalText = %q, want smart-tier response", res.FinalText)
	}
	if len(cheap.Requests) != 1 {
		t.Fatalf("cheap requests = %d, want 1", len(cheap.Requests))
	}
	if len(smart.Requests) != 1 {
		t.Fatalf("smart requests = %d, want 1", len(smart.Requests))
	}
}

func TestRouterSolverEscalatesRealMutatingTool(t *testing.T) {
	ctx := context.Background()
	cheap := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "Write the file.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "fs_write", Args: json.RawMessage(`{"path":"note.txt","content":"hello"}`)},
				},
			},
		},
	}
	smart := &MockProvider{
		Responses: []providers.CompleteResponse{
			{AssistantText: "Using smart tier for mutation."},
		},
	}
	loop := newRouterTestLoop(t, cheap, smart, []string{"fs_write"})

	res, err := loop.Solver.Solve(ctx, loop, "Write note.txt")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if res.FinalText != "Using smart tier for mutation." {
		t.Fatalf("FinalText = %q, want smart-tier response", res.FinalText)
	}
	if len(cheap.Requests) != 1 {
		t.Fatalf("cheap requests = %d, want 1", len(cheap.Requests))
	}
	if len(smart.Requests) != 1 {
		t.Fatalf("smart requests = %d, want 1", len(smart.Requests))
	}
	if _, err := os.Stat(filepath.Join(loop.Run.Dir, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected fs_write not to run before escalation, stat err = %v", err)
	}
}

func TestRouterSolverThresholdHookIgnoresToolFreeTurn(t *testing.T) {
	ctx := context.Background()
	cheap := &MockProvider{
		Responses: []providers.CompleteResponse{
			{AssistantText: "No tools needed."},
		},
	}
	smart := &MockProvider{}
	loop := newRouterTestLoop(t, cheap, smart, nil)
	loop.Hooks = append(loop.Hooks, ThresholdHook(1))

	res, err := loop.Solver.Solve(ctx, loop, "Say hi")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if res.FinalText != "No tools needed." {
		t.Fatalf("FinalText = %q, want no-tool response", res.FinalText)
	}
	if len(cheap.Requests) != 1 {
		t.Fatalf("cheap requests = %d, want 1", len(cheap.Requests))
	}
}

func TestRouterSolverDeduplicationHookWarnsBeforeThirdRequest(t *testing.T) {
	ctx := context.Background()
	cheap := &MockProvider{
		Responses: []providers.CompleteResponse{
			{
				AssistantText: "Create the directory.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "fs_mkdir", Args: json.RawMessage(`{"path":"subdir"}`)},
				},
			},
			{
				AssistantText: "Create it again.",
				ToolCalls: []providers.ToolCall{
					{ID: "call_2", Name: "fs_mkdir", Args: json.RawMessage(`{"path":"subdir"}`)},
				},
			},
			{AssistantText: "I already created it."},
		},
	}
	smart := &MockProvider{}
	loop := newRouterTestLoop(t, cheap, smart, []string{"fs_mkdir"})
	loop.Hooks = append(loop.Hooks, DeduplicationHook(2))

	res, err := loop.Solver.Solve(ctx, loop, "Create subdir")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if res.FinalText != "I already created it." {
		t.Fatalf("FinalText = %q, want dedup-aware response", res.FinalText)
	}
	if len(cheap.Requests) != 3 {
		t.Fatalf("cheap requests = %d, want 3", len(cheap.Requests))
	}
	if len(smart.Requests) != 0 {
		t.Fatalf("smart requests = %d, want 0", len(smart.Requests))
	}

	foundWarning := false
	for _, msg := range cheap.Requests[2].Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "DEDUPLICATION WARNING") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected dedup warning in third request, got %+v", cheap.Requests[2].Messages)
	}
}

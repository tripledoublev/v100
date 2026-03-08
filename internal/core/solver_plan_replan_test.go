package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type scriptedProvider struct {
	steps []providerStep
	idx   int
}

type providerStep struct {
	resp providers.CompleteResponse
	err  error
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true}
}
func (p *scriptedProvider) Complete(_ context.Context, _ providers.CompleteRequest) (providers.CompleteResponse, error) {
	if p.idx >= len(p.steps) {
		return providers.CompleteResponse{AssistantText: "done"}, nil
	}
	step := p.steps[p.idx]
	p.idx++
	return step.resp, step.err
}
func (p *scriptedProvider) Embed(_ context.Context, _ providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{Embedding: []float32{0.1, 0.2}}, nil
}

func TestPlanExecuteSolverRestoresCheckpointOnReplan(t *testing.T) {
	workspace := t.TempDir()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")

	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trace, err := OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()

	prov := &scriptedProvider{
		steps: []providerStep{
			{resp: providers.CompleteResponse{AssistantText: "1. Update state.txt"}},
			{resp: providers.CompleteResponse{ToolCalls: []providers.ToolCall{
				{ID: "call-1", Name: "fs_write", Args: json.RawMessage(`{"path":"state.txt","content":"after\n"}`)},
			}}},
			{err: errors.New("provider failed during execution")},
			{resp: providers.CompleteResponse{AssistantText: "diagnostic assistance"}},
			{resp: providers.CompleteResponse{AssistantText: "1. Verify and finish without edits"}},
			{resp: providers.CompleteResponse{AssistantText: "Recovered successfully."}},
		},
	}

	reg := tools.NewRegistry([]string{"fs_write"})
	reg.Register(tools.FSWrite())

	loop := &Loop{
		Run:       &Run{ID: "plan-replan", Dir: workspace, TraceFile: tracePath},
		Provider:  prov,
		Tools:     reg,
		Policy:    policy.Default(),
		Trace:     trace,
		Budget:    NewBudgetTracker(&Budget{MaxSteps: 10, MaxTokens: 10000}),
		ConfirmFn: func(_, _ string) bool { return true },
		Solver:    &PlanExecuteSolver{MaxReplans: 1},
		Mapper:    NewPathMapper(workspace, workspace),
		Snapshots: NewWorkspaceSnapshotManager(workspace, snapshotRoot),
	}

	res, err := loop.Solver.Solve(context.Background(), loop, "update the state file")
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if res.FinalText != "Recovered successfully." {
		t.Fatalf("final text = %q, want recovery success", res.FinalText)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "before\n" {
		t.Fatalf("workspace content after restore = %q, want before", content)
	}

	events, err := ReadAll(trace.Path())
	if err != nil {
		t.Fatal(err)
	}
	hasRestore := false
	hasReplan := false
	for _, ev := range events {
		switch ev.Type {
		case EventSandboxRestore:
			hasRestore = true
		case EventSolverReplan:
			var payload SolverReplanPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Attempt == 1 && payload.Plan != "" {
				hasReplan = true
			}
		}
	}
	if !hasRestore {
		t.Fatal("expected sandbox.restore event during replan")
	}
	if !hasReplan {
		t.Fatal("expected solver.replan event with revised plan")
	}
}

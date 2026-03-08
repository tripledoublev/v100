package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockSnapshotManager struct {
	captures []core.SnapshotRequest
	restores []core.RestoreRequest
}

func (m *mockSnapshotManager) Capture(_ context.Context, req core.SnapshotRequest) (core.SnapshotResult, error) {
	m.captures = append(m.captures, req)
	return core.SnapshotResult{ID: "snap-" + req.CallID, Method: "mock"}, nil
}

func (m *mockSnapshotManager) Restore(_ context.Context, req core.RestoreRequest) (core.RestoreResult, error) {
	m.restores = append(m.restores, req)
	return core.RestoreResult{SnapshotID: req.SnapshotID, Method: "mock"}, nil
}

type mockDangerousTool struct {
	calls int
}

func (t *mockDangerousTool) Name() string        { return "mock_dangerous" }
func (t *mockDangerousTool) Description() string { return "Dangerous tool without workspace mutation." }
func (t *mockDangerousTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *mockDangerousTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)
}
func (t *mockDangerousTool) DangerLevel() tools.DangerLevel {
	return tools.Dangerous
}
func (t *mockDangerousTool) Effects() tools.ToolEffects { return tools.ToolEffects{} }
func (t *mockDangerousTool) Exec(_ context.Context, _ tools.ToolCallContext, _ json.RawMessage) (tools.ToolResult, error) {
	t.calls++
	return tools.ToolResult{OK: true, Output: "ok"}, nil
}

func TestLoopSnapshotsBeforeWorkspaceMutations(t *testing.T) {
	t.Run("fs_mkdir", func(t *testing.T) {
		prov := &mockProvider{
			responses: []providers.CompleteResponse{
				{
					ToolCalls: []providers.ToolCall{
						{ID: "call-1", Name: "fs_mkdir", Args: json.RawMessage(`{"path":"subdir"}`)},
					},
				},
				{AssistantText: "done"},
			},
		}

		loop, trace, dir, snaps := newSnapshotTestLoop(t, prov, []string{"fs_mkdir"})
		defer func() { _ = trace.Close() }()
		loop.Tools.Register(tools.FSMkdir())

		if err := loop.Step(context.Background(), "make dir"); err != nil {
			t.Fatal(err)
		}
		if len(snaps.captures) != 1 {
			t.Fatalf("snapshot captures = %d, want 1", len(snaps.captures))
		}
		if snaps.captures[0].ToolName != "fs_mkdir" {
			t.Fatalf("snapshot tool = %q, want fs_mkdir", snaps.captures[0].ToolName)
		}
		if _, err := os.Stat(filepath.Join(dir, "subdir")); err != nil {
			t.Fatalf("expected directory to exist after fs_mkdir: %v", err)
		}
		assertSnapshotEvent(t, trace.Path(), "call-1", "fs_mkdir")
	})

	t.Run("patch_apply", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		prov := &mockProvider{
			responses: []providers.CompleteResponse{
				{
					ToolCalls: []providers.ToolCall{
						{ID: "call-2", Name: "patch_apply", Args: json.RawMessage(`{"diff":"--- target.txt\n+++ target.txt\n@@ -1 +1 @@\n-old\n+new\n","strip":0}`)},
					},
				},
				{AssistantText: "done"},
			},
		}

		tracePath := filepath.Join(dir, "trace.jsonl")
		trace, err := core.OpenTrace(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = trace.Close() }()

		snaps := &mockSnapshotManager{}
		reg := tools.NewRegistry([]string{"patch_apply"})
		reg.Register(tools.PatchApply())
		loop := &core.Loop{
			Run:       &core.Run{ID: "patch-run", Dir: dir, TraceFile: tracePath},
			Provider:  prov,
			Tools:     reg,
			Policy:    policy.Default(),
			Trace:     trace,
			Budget:    core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
			ConfirmFn: func(_, _ string) bool { return true },
			Mapper:    core.NewPathMapper(dir, dir),
			Snapshots: snaps,
		}

		if err := loop.Step(context.Background(), "patch file"); err != nil {
			t.Fatal(err)
		}
		if len(snaps.captures) != 1 {
			t.Fatalf("snapshot captures = %d, want 1", len(snaps.captures))
		}
		if snaps.captures[0].ToolName != "patch_apply" {
			t.Fatalf("snapshot tool = %q, want patch_apply", snaps.captures[0].ToolName)
		}
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "new\n" {
			t.Fatalf("patched content = %q, want new", content)
		}
		assertSnapshotEvent(t, trace.Path(), "call-2", "patch_apply")
	})
}

func TestLoopDoesNotSnapshotDangerousNonMutatingTool(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-3", Name: "mock_dangerous", Args: json.RawMessage(`{}`)},
				},
			},
			{AssistantText: "done"},
		},
	}

	loop, trace, _, snaps := newSnapshotTestLoop(t, prov, []string{"mock_dangerous"})
	defer func() { _ = trace.Close() }()

	dangerousTool := &mockDangerousTool{}
	loop.Tools.Register(dangerousTool)

	if err := loop.Step(context.Background(), "dangerous"); err != nil {
		t.Fatal(err)
	}
	if dangerousTool.calls != 1 {
		t.Fatalf("dangerous tool calls = %d, want 1", dangerousTool.calls)
	}
	if len(snaps.captures) != 0 {
		t.Fatalf("snapshot captures = %d, want 0", len(snaps.captures))
	}

	events, err := core.ReadAll(trace.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == core.EventSandboxSnapshot {
			t.Fatalf("unexpected sandbox.snapshot event: %s", ev.Payload)
		}
	}
}

func newSnapshotTestLoop(t *testing.T, prov providers.Provider, enabledTools []string) (*core.Loop, *core.TraceWriter, string, *mockSnapshotManager) {
	t.Helper()

	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}

	snaps := &mockSnapshotManager{}
	reg := tools.NewRegistry(enabledTools)
	loop := &core.Loop{
		Run:       &core.Run{ID: "snapshot-run", Dir: dir, TraceFile: tracePath},
		Provider:  prov,
		Tools:     reg,
		Policy:    policy.Default(),
		Trace:     trace,
		Budget:    core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
		ConfirmFn: func(_, _ string) bool { return true },
		Mapper:    core.NewPathMapper(dir, dir),
		Snapshots: snaps,
	}
	return loop, trace, dir, snaps
}

func assertSnapshotEvent(t *testing.T, tracePath, callID, name string) {
	t.Helper()

	events, err := core.ReadAll(tracePath)
	if err != nil {
		t.Fatal(err)
	}

	for _, ev := range events {
		if ev.Type != core.EventSandboxSnapshot {
			continue
		}
		var payload core.SandboxSnapshotPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.CallID == callID && payload.Name == name {
			if payload.Method != "mock" {
				t.Fatalf("snapshot method = %q, want mock", payload.Method)
			}
			return
		}
	}
	t.Fatalf("expected sandbox.snapshot event for %s/%s", callID, name)
}

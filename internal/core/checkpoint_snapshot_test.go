package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestCheckpointRestoresWorkspaceState(t *testing.T) {
	workspace := t.TempDir()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	loop := &core.Loop{
		Run:       &core.Run{ID: "cp-run", Dir: workspace, TraceFile: tracePath},
		Trace:     trace,
		Messages:  []providers.Message{{Role: "user", Content: "before"}},
		Snapshots: core.NewWorkspaceSnapshotManager(workspace, filepath.Join(filepath.Dir(workspace), "snapshots")),
	}

	cp, err := loop.CheckpointWithContext(context.Background())
	if err != nil {
		t.Fatalf("CheckpointWithContext returned error: %v", err)
	}
	if cp.ID == "" {
		t.Fatal("expected checkpoint id to be populated")
	}
	if cp.SnapshotID == "" {
		t.Fatal("expected checkpoint snapshot id to be populated")
	}
	stored, err := core.ReadCheckpoint(filepath.Dir(tracePath), cp.ID)
	if err != nil {
		t.Fatalf("ReadCheckpoint returned error: %v", err)
	}
	if stored.SnapshotID != cp.SnapshotID {
		t.Fatalf("stored snapshot id = %q, want %q", stored.SnapshotID, cp.SnapshotID)
	}
	if len(stored.Messages) != 1 || stored.Messages[0].Content != "before" {
		t.Fatalf("stored checkpoint messages = %+v", stored.Messages)
	}

	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loop.Messages = append(loop.Messages, providers.Message{Role: "assistant", Content: "after"})
	loop.Messages = append(loop.Messages, providers.Message{Role: "assistant", Content: "more"})

	if err := loop.RestoreWithContext(context.Background(), cp); err != nil {
		t.Fatalf("RestoreWithContext returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(workspace, "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "before\n" {
		t.Fatalf("restored content = %q, want before", content)
	}
	if len(loop.Messages) != 1 || loop.Messages[0].Content != "before" {
		t.Fatalf("restored messages = %+v, want single original message", loop.Messages)
	}

	assertRestoreEvent(t, trace.Path(), cp.SnapshotID)
}

func TestCheckpointSnapshotSkipsCacheDirectories(t *testing.T) {
	workspace := t.TempDir()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	if err := os.MkdirAll(filepath.Join(workspace, ".cache", "go-mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".cache", "go-mod", "cache.txt"), []byte("ignore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "state.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	snapshotRoot := filepath.Join(filepath.Dir(workspace), "snapshots")
	loop := &core.Loop{
		Run:       &core.Run{ID: "cp-run", Dir: workspace, TraceFile: tracePath},
		Trace:     trace,
		Snapshots: core.NewWorkspaceSnapshotManager(workspace, snapshotRoot),
	}

	cp, err := loop.CheckpointWithContext(context.Background())
	if err != nil {
		t.Fatalf("CheckpointWithContext returned error: %v", err)
	}

	snapPath := filepath.Join(snapshotRoot, cp.SnapshotID)
	if _, err := os.Stat(filepath.Join(snapPath, ".cache")); !os.IsNotExist(err) {
		t.Fatalf("expected .cache to be excluded from snapshot, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(snapPath, "state.txt")); err != nil {
		t.Fatalf("expected state.txt in snapshot: %v", err)
	}
}

func assertRestoreEvent(t *testing.T, tracePath, snapshotID string) {
	t.Helper()

	events, err := core.ReadAll(tracePath)
	if err != nil {
		t.Fatal(err)
	}

	hasSnapshot := false
	hasRestore := false
	for _, ev := range events {
		switch ev.Type {
		case core.EventSandboxSnapshot:
			var payload core.SandboxSnapshotPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.SnapshotID == snapshotID && payload.Reason == "checkpoint" {
				hasSnapshot = true
			}
		case core.EventSandboxRestore:
			var payload core.SandboxRestorePayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.SnapshotID == snapshotID && payload.Reason == "checkpoint" {
				hasRestore = true
			}
		}
	}
	if !hasSnapshot {
		t.Fatalf("expected sandbox.snapshot event for checkpoint %q", snapshotID)
	}
	if !hasRestore {
		t.Fatalf("expected sandbox.restore event for checkpoint %q", snapshotID)
	}
}

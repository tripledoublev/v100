package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestResolveCheckpointForRestoreUsesLatestByDefault(t *testing.T) {
	runDir := t.TempDir()
	first := core.Checkpoint{
		ID:         "snap-1",
		SnapshotID: "snap-1",
		CreatedAt:  time.Unix(10, 0).UTC(),
	}
	second := core.Checkpoint{
		ID:         "snap-2",
		SnapshotID: "snap-2",
		CreatedAt:  time.Unix(20, 0).UTC(),
	}
	if err := core.PersistCheckpoint(runDir, first); err != nil {
		t.Fatal(err)
	}
	if err := core.PersistCheckpoint(runDir, second); err != nil {
		t.Fatal(err)
	}

	cp, err := resolveCheckpointForRestore(runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID != "snap-2" {
		t.Fatalf("latest checkpoint id = %q, want snap-2", cp.ID)
	}
}

func TestReconstructHistoryResetsOnSandboxRestore(t *testing.T) {
	runDir := t.TempDir()
	checkpoint := core.Checkpoint{
		ID:         "snap-restore",
		SnapshotID: "snap-restore",
		CreatedAt:  time.Now().UTC(),
		Messages: []providers.Message{
			{Role: "user", Content: "before restore"},
			{Role: "assistant", Content: "checkpoint state"},
		},
		StepCount: 2,
	}
	if err := core.PersistCheckpoint(runDir, checkpoint); err != nil {
		t.Fatal(err)
	}

	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "mock",
			Model:     "test",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{Content: "old user"}),
		mustEvent(t, core.EventModelResp, core.ModelRespPayload{Text: "old assistant"}),
		mustEvent(t, core.EventSandboxRestore, core.SandboxRestorePayload{
			SnapshotID: "snap-restore",
			Method:     "full_copy",
			Reason:     "manual_restore",
		}),
		mustEvent(t, core.EventToolResult, core.ToolResultPayload{
			CallID: "call-1",
			Name:   "fs_read",
			OK:     true,
			Output: "after restore tool output",
		}),
	}

	msgs, providerName, model, workspace := reconstructHistory(runDir, events)
	if providerName != "mock" || model != "test" || workspace != "/workspace" {
		t.Fatalf("unexpected run info: provider=%q model=%q workspace=%q", providerName, model, workspace)
	}
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3 (%+v)", len(msgs), msgs)
	}
	if msgs[0].Content != "before restore" || msgs[1].Content != "checkpoint state" {
		t.Fatalf("restore did not reset message history: %+v", msgs)
	}
	if msgs[2].Content != "after restore tool output" || msgs[2].Role != "tool" {
		t.Fatalf("unexpected post-restore message: %+v", msgs[2])
	}
}

func mustEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		Type:    typ,
		Payload: b,
	}
}

func TestCheckpointStorePathSanitizesID(t *testing.T) {
	runDir := t.TempDir()
	cp := core.Checkpoint{
		ID:         "snap/with/slash",
		SnapshotID: "snap/with/slash",
		CreatedAt:  time.Now().UTC(),
	}
	if err := core.PersistCheckpoint(runDir, cp); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadCheckpoint(runDir, cp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs(runDir); err != nil {
		t.Fatal(err)
	}
}

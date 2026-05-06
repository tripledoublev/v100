package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestGraphCmdRegistered(t *testing.T) {
	cmd := rootCmd()
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "graph" {
			found = true
			if child.Flags().Lookup("output") == nil {
				t.Fatal("graph command missing --output flag")
			}
		}
	}
	if !found {
		t.Fatal("graph command not registered")
	}
}

func TestBuildTraceDAGMarksSnapshotAndRestore(t *testing.T) {
	runDir := t.TempDir()
	snapshotDir := filepath.Join(runDir, "snapshots", "snap-1")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "state.txt"), []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	events := []core.Event{
		graphEvent(t, core.EventRunStart, "s1", "e1", core.RunStartPayload{Provider: "test"}),
		graphEvent(t, core.EventSandboxSnapshot, "s1", "e2", core.SandboxSnapshotPayload{SnapshotID: "snap-1", Method: "full_copy"}),
		graphEvent(t, core.EventToolCall, "s1", "e3", core.ToolCallPayload{CallID: "c1", Name: "fs_write", Args: `{}`}),
		graphEvent(t, core.EventSandboxRestore, "s2", "e4", core.SandboxRestorePayload{SnapshotID: "snap-1", Method: "full_copy"}),
	}

	nodes, edges := buildTraceDAG(runDir, events)
	if len(nodes) != 4 {
		t.Fatalf("nodes len = %d, want 4", len(nodes))
	}
	if len(edges) != 4 {
		t.Fatalf("edges len = %d, want 4", len(edges))
	}
	if nodes[1].SnapshotID != "snap-1" || !strings.Contains(nodes[1].WorkspaceState, "state.txt") {
		t.Fatalf("snapshot node = %#v", nodes[1])
	}
	if nodes[3].Type != string(core.EventSandboxRestore) || nodes[3].SnapshotID != "snap-1" {
		t.Fatalf("restore node = %#v", nodes[3])
	}
	if edges[3].Kind != "restore" || edges[3].From != nodes[1].ID || edges[3].To != nodes[3].ID {
		t.Fatalf("restore edge = %#v", edges[3])
	}
}

func TestRenderTraceDAGHTMLContainsInteractivePanel(t *testing.T) {
	doc, err := renderTraceDAGHTML("run-1", t.TempDir(), []core.Event{
		graphEvent(t, core.EventRunStart, "s1", "e1", core.RunStartPayload{Provider: "test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Trace DAG", "click any node", "Workspace State", "addEventListener('click'"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func graphEvent(t *testing.T, typ core.EventType, stepID, eventID string, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		TS:      time.Unix(1, 0).UTC(),
		RunID:   "run-1",
		StepID:  stepID,
		EventID: eventID,
		Type:    typ,
		Payload: b,
	}
}

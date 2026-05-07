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

func TestBuildTraceDAGGroupsContiguousModelTokens(t *testing.T) {
	events := []core.Event{
		graphEvent(t, core.EventModelCall, "s1", "call-1", core.ModelCallPayload{Model: "test"}),
		graphEvent(t, core.EventModelToken, "s1", "tok-1", map[string]string{"text": "hel"}),
		graphEvent(t, core.EventModelToken, "s1", "tok-2", map[string]string{"text": "lo"}),
		graphEvent(t, core.EventModelResp, "s1", "resp-1", core.ModelRespPayload{Text: "hello"}),
		graphEvent(t, core.EventModelToken, "s2", "tok-3", map[string]string{"text": "bye"}),
	}

	nodes, edges := buildTraceDAG(t.TempDir(), events)
	if len(nodes) != 4 {
		t.Fatalf("nodes len = %d, want 4 after grouping contiguous model.token events", len(nodes))
	}
	if nodes[1].Type != string(core.EventModelToken) {
		t.Fatalf("grouped token node type = %q, want model.token", nodes[1].Type)
	}
	if !strings.Contains(nodes[1].Label, "model.token x2") {
		t.Fatalf("grouped token label = %q, want count", nodes[1].Label)
	}
	if !strings.Contains(nodes[1].Payload, `"count": 2`) || !strings.Contains(nodes[1].Payload, "hello") {
		t.Fatalf("grouped token payload = %q, want count and merged text", nodes[1].Payload)
	}
	if nodes[1].ReplayEventID != "tok-1" {
		t.Fatalf("grouped token replay event id = %q, want first original token id", nodes[1].ReplayEventID)
	}
	if len(edges) != 3 {
		t.Fatalf("edges len = %d, want 3 timeline edges after grouping", len(edges))
	}
}

func TestRenderTraceDAGHTMLContainsInteractivePanel(t *testing.T) {
	doc, err := renderTraceDAGHTML("run-1", t.TempDir(), []core.Event{
		graphEvent(t, core.EventRunStart, "s1", "e1", core.RunStartPayload{Provider: "test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Trace DAG", "click any node", "Workspace State", "Replay", "v100 replay", "--from-event", "copyReplay", "v100://replay", "addEventListener('click'"} {
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

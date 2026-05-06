package tools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestProvenanceLookupFindsLineInRunTrace(t *testing.T) {
	workspace := t.TempDir()
	runID := "run-1"
	runDir := filepath.Join(workspace, "runs", runID)
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []core.Event{
		provToolEvent(t, runID, "s1", "m1", core.EventModelResp, core.ModelRespPayload{Text: "create the target"}),
		provToolEvent(t, runID, "s1", "tc1", core.EventToolCall, core.ToolCallPayload{CallID: "c1", Name: "fs_write", Args: `{"path":"target.txt","content":"a\nb\n"}`}),
		provToolEvent(t, runID, "s1", "tr1", core.EventToolResult, core.ToolResultPayload{CallID: "c1", Name: "fs_write", OK: true, Output: `{"bytes_written":4}`}),
	} {
		if err := trace.Write(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}

	res, err := tools.ProvenanceLookup().Exec(context.Background(), tools.ToolCallContext{
		RunID:        runID,
		WorkspaceDir: workspace,
	}, json.RawMessage(`{"path":"target.txt","line":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("lookup failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"call_id": "c1"`) {
		t.Fatalf("output missing call id: %s", res.Output)
	}
	if !strings.Contains(res.Output, "create the target") {
		t.Fatalf("output missing reasoning: %s", res.Output)
	}
}

func TestProvenanceLookupErrorsWhenRunMissing(t *testing.T) {
	workspace := t.TempDir()
	res, err := tools.ProvenanceLookup().Exec(context.Background(), tools.ToolCallContext{
		WorkspaceDir: workspace,
	}, json.RawMessage(`{"path":"target.txt","run_id":"missing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected missing run failure, got %s", res.Output)
	}
	if !strings.Contains(res.Output, "not found") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
}

func provToolEvent(t *testing.T, runID, stepID, eventID string, typ core.EventType, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		TS:      time.Unix(1, 0).UTC(),
		RunID:   runID,
		StepID:  stepID,
		EventID: eventID,
		Type:    typ,
		Payload: b,
	}
}

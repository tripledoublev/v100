package eval

import (
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestDiffTraces(t *testing.T) {
	runA := "run-a"
	runB := "run-b"

	// Scenario 1: Identical traces
	events1 := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
	}
	diff := DiffTraces(runA, runB, events1, events1)
	if diff.DivergeType != "none" {
		t.Errorf("expected no divergence, got %s", diff.DivergeType)
	}

	// Scenario 2: Tool choice divergence
	tcA, _ := json.Marshal(core.ToolCallPayload{Name: "fs_read"})
	tcB, _ := json.Marshal(core.ToolCallPayload{Name: "fs_list"})
	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall, Payload: tcA},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall, Payload: tcB},
	}

	diff = DiffTraces(runA, runB, eventsA, eventsB)
	if diff.DivergeType != "tool_choice" {
		t.Errorf("expected tool_choice divergence, got %s", diff.DivergeType)
	}
	if diff.CommonPrefix != 1 {
		t.Errorf("expected common prefix 1, got %d", diff.CommonPrefix)
	}

	// Scenario 3: Plan divergence
	plA, _ := json.Marshal(map[string]string{"plan": "step 1"})
	plB, _ := json.Marshal(map[string]string{"plan": "step X"})
	eventsPlanA := []core.Event{{Type: core.EventSolverPlan, Payload: plA}}
	eventsPlanB := []core.Event{{Type: core.EventSolverPlan, Payload: plB}}

	diff = DiffTraces(runA, runB, eventsPlanA, eventsPlanB)
	if diff.DivergeType != "plan_diff" {
		t.Errorf("expected plan_diff, got %s", diff.DivergeType)
	}
}

func TestDiffTracesUsesSynchronizedAlignment(t *testing.T) {
	runA := "run-a"
	runB := "run-b"

	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall, Payload: mustDiffJSON(t, core.ToolCallPayload{Name: "fs_read"})},
		{Type: core.EventRunEnd},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventRunEnd},
	}

	diff := DiffTraces(runA, runB, eventsA, eventsB)
	if diff.DivergeType != "length_mismatch" {
		t.Fatalf("expected length_mismatch, got %s", diff.DivergeType)
	}
	if diff.DivergeStep != 1 {
		t.Fatalf("expected divergence at aligned step 1, got %d", diff.DivergeStep)
	}
	if diff.CommonPrefix != 1 {
		t.Fatalf("expected common prefix 1, got %d", diff.CommonPrefix)
	}
}

func mustDiffJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

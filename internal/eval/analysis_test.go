package eval

import (
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestAnalyzeTrajectoryIncludesCoreClassification(t *testing.T) {
	events := []core.Event{
		{Type: core.EventModelCall},
		{Type: core.EventRunEnd, Payload: mustJSON(t, core.RunEndPayload{Reason: "budget_tokens"})},
	}

	report := AnalyzeTrajectory(events)
	if report.Efficiency != 40 {
		t.Fatalf("efficiency = %.2f, want 40", report.Efficiency)
	}
	if !hasLabel(report.Labels, "budget_exhaustion") {
		t.Fatalf("expected budget_exhaustion label, got %+v", report.Labels)
	}
}

func TestAnalyzeTrajectoryKeepsToolHallucinationLabel(t *testing.T) {
	events := []core.Event{
		{
			Type:    core.EventToolResult,
			Payload: mustJSON(t, core.ToolResultPayload{Name: "fs_write", OK: false, Output: `tool "fs_write" not found or not enabled`}),
		},
		{Type: core.EventRunEnd, Payload: mustJSON(t, core.RunEndPayload{Reason: "user_exit"})},
	}

	report := AnalyzeTrajectory(events)
	if report.ToolErrors != 1 {
		t.Fatalf("tool errors = %d, want 1", report.ToolErrors)
	}
	if !hasLabel(report.Labels, "tool_hallucination") {
		t.Fatalf("expected tool_hallucination label, got %+v", report.Labels)
	}
}

func hasLabel(labels []BehaviorLabel, want string) bool {
	for _, label := range labels {
		if label.Name == want {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

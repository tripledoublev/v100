package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

func TestComputeStatsCapturesModelMetadata(t *testing.T) {
	payload, err := json.Marshal(RunStartPayload{
		Provider: "codex",
		Model:    "gpt-5.4",
		ModelMetadata: providers.ModelMetadata{
			Model:       "gpt-5.4",
			ContextSize: 128000,
			IsFree:      true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := ComputeStats([]Event{{
		TS:      time.Unix(0, 0),
		RunID:   "run-1",
		Type:    EventRunStart,
		Payload: payload,
	}})

	if stats.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", stats.Provider)
	}
	if stats.ModelMetadata.ContextSize != 128000 {
		t.Fatalf("context size = %d, want 128000", stats.ModelMetadata.ContextSize)
	}
	if !stats.ModelMetadata.IsFree {
		t.Fatal("expected free model metadata to be captured")
	}
}

func TestFormatStatsIncludesMetadata(t *testing.T) {
	out := FormatStats(RunStats{
		RunID:         "run-1",
		Provider:      "openai",
		Model:         "gpt-4.1",
		ModelMetadata: providers.ModelMetadata{ContextSize: 128000, CostPer1MIn: 2.5, CostPer1MOut: 10},
	})

	for _, want := range []string{
		"Context:      128k",
		"Pricing:      $2.50/$10.00",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatStats() missing %q in output:\n%s", want, out)
		}
	}
}

func TestFormatCompareIncludesMetadata(t *testing.T) {
	out := FormatCompare([]RunStats{
		{
			RunID:         "run-1",
			Provider:      "codex",
			Model:         "gpt-5.4",
			ModelMetadata: providers.ModelMetadata{ContextSize: 128000, IsFree: true},
		},
		{
			RunID:         "run-2",
			Provider:      "openai",
			Model:         "gpt-4.1",
			ModelMetadata: providers.ModelMetadata{ContextSize: 128000, CostPer1MIn: 2.5, CostPer1MOut: 10},
		},
	})

	for _, want := range []string{
		"Provider",
		"Model",
		"Context",
		"Pricing",
		"free",
		"$2.50/$10.00",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatCompare() missing %q in output:\n%s", want, out)
		}
	}
}

func TestComputeStatsToolRetries(t *testing.T) {
	now := time.Now()
	mkToolCall := func(stepID, name string) Event {
		p, _ := json.Marshal(ToolCallPayload{Name: name})
		return Event{TS: now, StepID: stepID, Type: EventToolCall, Payload: p}
	}
	mkToolResult := func(stepID, name string, ok bool) Event {
		p, _ := json.Marshal(ToolResultPayload{Name: name, OK: ok})
		return Event{TS: now, StepID: stepID, Type: EventToolResult, Payload: p}
	}
	mkStepSummary := func(stepID string, n int) Event {
		p, _ := json.Marshal(StepSummaryPayload{StepNumber: n})
		return Event{TS: now, StepID: stepID, Type: EventStepSummary, Payload: p}
	}

	events := []Event{
		// sh fails, then sh is retried (1 retry)
		mkToolCall("s1", "sh"),
		mkToolResult("s1", "sh", false),
		mkToolCall("s1", "sh"),
		mkToolResult("s1", "sh", true),
		// fs_read fails, then a different tool is called (0 retries)
		mkToolCall("s2", "fs_read"),
		mkToolResult("s2", "fs_read", false),
		mkToolCall("s2", "search"),
		mkToolResult("s2", "search", true),
		// sh fails twice, retried once each time (2 retries)
		mkToolCall("s3", "sh"),
		mkToolResult("s3", "sh", false),
		mkToolCall("s3", "sh"),
		mkToolResult("s3", "sh", false),
		mkToolCall("s3", "sh"),
		mkToolResult("s3", "sh", true),
		// A later step using the same tool should not count as retry.
		mkToolCall("s4", "sh"),
		mkToolResult("s4", "sh", false),
		mkStepSummary("s4", 4),
		mkToolCall("s5", "sh"),
		mkToolResult("s5", "sh", true),
	}

	s := ComputeStats(events)
	if s.ToolRetries != 3 {
		t.Fatalf("ToolRetries = %d, want 3", s.ToolRetries)
	}
	if s.ToolFailures != 5 {
		t.Fatalf("ToolFailures = %d, want 5", s.ToolFailures)
	}
}

func TestFormatStatsToolRetries(t *testing.T) {
	out := FormatStats(RunStats{ToolRetries: 2})
	if !strings.Contains(out, "Tool retries: 2") {
		t.Fatalf("expected 'Tool retries: 2' in output:\n%s", out)
	}

	out = FormatStats(RunStats{ToolRetries: 0})
	if strings.Contains(out, "Tool retries") {
		t.Fatalf("expected no 'Tool retries' line when 0, got:\n%s", out)
	}
}

func TestComputeStatsDoesNotDeduplicateCallIDsAcrossSteps(t *testing.T) {
	now := time.Now()
	mkToolCall := func(stepID, callID, name string) Event {
		p, _ := json.Marshal(ToolCallPayload{CallID: callID, Name: name})
		return Event{TS: now, StepID: stepID, Type: EventToolCall, Payload: p}
	}

	events := []Event{
		mkToolCall("s1", "call-1", "fs_read"),
		mkToolCall("s2", "call-1", "fs_read"),
	}

	s := ComputeStats(events)
	if s.ToolCalls != 2 {
		t.Fatalf("ToolCalls = %d, want 2", s.ToolCalls)
	}
	if s.ToolUsage["fs_read"] != 2 {
		t.Fatalf("ToolUsage[fs_read] = %d, want 2", s.ToolUsage["fs_read"])
	}
}

func TestComputeDigestDoesNotDeduplicateCallIDsAcrossSteps(t *testing.T) {
	now := time.Now()
	mkToolCall := func(stepID, callID, name string) Event {
		p, _ := json.Marshal(ToolCallPayload{CallID: callID, Name: name})
		return Event{TS: now, StepID: stepID, Type: EventToolCall, Payload: p}
	}

	events := []Event{
		mkToolCall("s1", "call-1", "fs_read"),
		mkToolCall("s2", "call-1", "fs_read"),
		mkToolCall("s3", "call-1", "fs_read"),
	}

	d := ComputeDigest(events)
	if len(d.RetryCluster) != 1 {
		t.Fatalf("RetryCluster len = %d, want 1", len(d.RetryCluster))
	}
	if d.RetryCluster[0].Name != "fs_read" || d.RetryCluster[0].Count != 3 {
		t.Fatalf("RetryCluster[0] = %+v, want fs_read x3", d.RetryCluster[0])
	}
}

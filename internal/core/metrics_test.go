package core

import (
	"encoding/json"
	"testing"
	"time"
)

func ev(t *testing.T, ts time.Time, typ EventType, stepID string, payload any) Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return Event{
		TS:      ts,
		RunID:   "run-1",
		StepID:  stepID,
		EventID: "e",
		Type:    typ,
		Payload: b,
	}
}

func TestComputeMetricsBasic(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		ev(t, now, EventRunStart, "s1", RunStartPayload{Provider: "codex", Model: "gpt-5.4"}),
		ev(t, now.Add(10*time.Millisecond), EventModelResp, "s1", ModelRespPayload{
			Text: "hi",
			Usage: Usage{
				InputTokens:  100,
				OutputTokens: 50,
				CostUSD:      0.01,
			},
			DurationMS: 1000,
		}),
		ev(t, now.Add(20*time.Millisecond), EventToolCall, "s1", ToolCallPayload{Name: "project_search", CallID: "c1"}),
		ev(t, now.Add(30*time.Millisecond), EventToolResult, "s1", ToolResultPayload{Name: "project_search", CallID: "c1", OK: true, DurationMS: 100}),
		ev(t, now.Add(40*time.Millisecond), EventCompress, "s1", CompressPayload{TokensBefore: 4000, TokensAfter: 1000}),
		ev(t, now.Add(45*time.Millisecond), EventAgentDispatch, "s1", AgentDispatchPayload{Agent: "researcher", Pattern: "pipeline"}),
		ev(t, now.Add(46*time.Millisecond), EventAgentEnd, "s1", AgentEndPayload{Agent: "researcher", OK: true, UsedTokens: 300, CostUSD: 0.02}),
		ev(t, now.Add(50*time.Millisecond), EventStepSummary, "s1", StepSummaryPayload{StepNumber: 1, DurationMS: 2000}),
		ev(t, now.Add(60*time.Millisecond), EventRunEnd, "s1", RunEndPayload{Reason: "completed"}),
	}

	m := ComputeMetrics(events)
	if m.StepsPerRun != 1 {
		t.Fatalf("expected steps=1, got %d", m.StepsPerRun)
	}
	if m.TokensPerRun != 150 {
		t.Fatalf("expected tokens=150, got %d", m.TokensPerRun)
	}
	if m.ToolCallSuccessRate != 1.0 {
		t.Fatalf("expected tool success rate 1.0, got %.2f", m.ToolCallSuccessRate)
	}
	if m.CompressionFrequency != 1.0 {
		t.Fatalf("expected compression frequency 1.0, got %.2f", m.CompressionFrequency)
	}
	if m.DispatchCalls != 1 || m.DispatchSuccessRate != 1.0 {
		t.Fatalf("expected dispatch metrics to be populated, got calls=%d success=%.2f", m.DispatchCalls, m.DispatchSuccessRate)
	}
}

func TestClassifyRunBudgetExhaustion(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		ev(t, now, EventRunStart, "s1", RunStartPayload{}),
		ev(t, now.Add(1*time.Millisecond), EventRunEnd, "s1", RunEndPayload{Reason: "budget_steps"}),
	}
	c := ClassifyRun(events)
	if c.Label != "budget_exhaustion" {
		t.Fatalf("expected budget_exhaustion, got %q", c.Label)
	}
}

func TestClassifyRunToolLoop(t *testing.T) {
	now := time.Now().UTC()
	events := []Event{
		ev(t, now, EventRunStart, "s1", RunStartPayload{}),
		ev(t, now.Add(1*time.Millisecond), EventToolCall, "s1", ToolCallPayload{Name: "project_search", CallID: "c1"}),
		ev(t, now.Add(2*time.Millisecond), EventToolCall, "s1", ToolCallPayload{Name: "project_search", CallID: "c2"}),
		ev(t, now.Add(3*time.Millisecond), EventToolCall, "s1", ToolCallPayload{Name: "project_search", CallID: "c3"}),
		ev(t, now.Add(4*time.Millisecond), EventToolCall, "s1", ToolCallPayload{Name: "project_search", CallID: "c4"}),
	}
	c := ClassifyRun(events)
	if c.Label != "tool_loop" {
		t.Fatalf("expected tool_loop, got %q", c.Label)
	}
}

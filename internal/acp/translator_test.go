package acp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestTranslatorMapsCoreEventsToSessionUpdates(t *testing.T) {
	var out bytes.Buffer
	conn := NewConn(strings.NewReader(""), &out)
	translate := NewTranslator(conn, "session-1")

	translate(acpEvent(t, core.EventModelToken, map[string]string{"text": "hello"}))
	translate(acpRawEvent(t, core.EventSolverPlan, "plan text"))
	translate(acpEvent(t, core.EventToolCall, core.ToolCallPayload{CallID: "call-1", Name: "fs_write", Args: `{"path":"a.txt"}`}))
	translate(acpEvent(t, core.EventToolResult, core.ToolResultPayload{CallID: "call-1", Name: "fs_write", OK: false, Output: "denied"}))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d notifications, want 4:\n%s", len(lines), out.String())
	}
	var got []SessionUpdateParams
	for _, line := range lines {
		var notif Notification
		if err := json.Unmarshal([]byte(line), &notif); err != nil {
			t.Fatalf("notification unmarshal: %v", err)
		}
		if notif.Method != MethodSessionUpdate {
			t.Fatalf("notification method = %q, want %q", notif.Method, MethodSessionUpdate)
		}
		var params SessionUpdateParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			t.Fatalf("params unmarshal: %v", err)
		}
		got = append(got, params)
	}
	if got[0].Update.Type != "agent_message_chunk" || got[0].Update.Content.Text != "hello" {
		t.Fatalf("model token update = %#v", got[0].Update)
	}
	if got[1].Update.Type != "agent_thought_chunk" || got[1].Update.Content.Text != "plan text" {
		t.Fatalf("plan update = %#v", got[1].Update)
	}
	if got[2].Update.Type != "tool_call" || got[2].Update.Kind != "edit" {
		t.Fatalf("tool call update = %#v", got[2].Update)
	}
	if got[3].Update.Type != "tool_call_update" || got[3].Update.Status != "failed" {
		t.Fatalf("tool result update = %#v", got[3].Update)
	}
}

func TestTranslatorMapsRuntimeLifecycleEvents(t *testing.T) {
	var out bytes.Buffer
	conn := NewConn(strings.NewReader(""), &out)
	translate := NewTranslator(conn, "session-1")

	translate(acpEvent(t, core.EventRunStart, core.RunStartPayload{Provider: "codex", Model: "gpt-5.4", Workspace: "/repo"}))
	translate(acpEvent(t, core.EventRunError, core.RunErrorPayload{Error: "provider failed"}))
	translate(acpEvent(t, core.EventStepSummary, core.StepSummaryPayload{StepNumber: 2, InputTokens: 10, OutputTokens: 5, ToolCalls: 1, ModelCalls: 1, DurationMS: 250}))
	translate(acpEvent(t, core.EventAgentStart, core.AgentStartPayload{Agent: "reviewer", AgentRunID: "child-1", ParentCallID: "call-1", Task: "review", Model: "gpt-5.4"}))
	translate(acpEvent(t, core.EventAgentDispatch, core.AgentDispatchPayload{Agent: "reviewer", AgentRunID: "child-1", ParentCallID: "call-1", Pattern: "pipeline", Task: "review"}))
	translate(acpEvent(t, core.EventAgentEnd, core.AgentEndPayload{Agent: "reviewer", AgentRunID: "child-1", ParentCallID: "call-1", OK: false, Result: "blocked"}))
	translate(acpEvent(t, core.EventHookIntervention, core.HookInterventionPayload{Action: "force_replan", Reason: "low confidence"}))
	translate(acpEvent(t, core.EventSandboxSnapshot, core.SandboxSnapshotPayload{SnapshotID: "snap-1", CallID: "call-2", Name: "patch_apply", Method: "delta"}))
	translate(acpEvent(t, core.EventSandboxRestore, core.SandboxRestorePayload{SnapshotID: "snap-1", Method: "delta", Reason: "rollback"}))
	translate(acpEvent(t, core.EventToolOutputDelta, core.ToolOutputDeltaPayload{CallID: "call-3", Name: "sh", Stream: "stderr", Delta: "working"}))
	translate(acpEvent(t, core.EventRunEnd, core.RunEndPayload{Reason: "budget_steps", UsedSteps: 2, UsedTokens: 15}))

	got := readACPUpdates(t, out.String())
	if len(got) != 11 {
		t.Fatalf("got %d notifications, want 11:\n%s", len(got), out.String())
	}
	checkUpdate := func(i int, typ, status string) {
		t.Helper()
		if got[i].Update.Type != typ || got[i].Update.Status != status {
			t.Fatalf("update %d = %#v, want type=%s status=%s", i, got[i].Update, typ, status)
		}
	}
	checkUpdate(0, "run_status_update", "in_progress")
	checkUpdate(1, "run_error", "failed")
	if got[1].Update.Title != "run error: provider failed" {
		t.Fatalf("run error title = %q", got[1].Update.Title)
	}
	checkUpdate(2, "step_summary", "completed")
	checkUpdate(3, "agent_lifecycle", "in_progress")
	if got[3].Update.ToolCallID != "child-1" {
		t.Fatalf("agent start ToolCallID = %q, want child-1", got[3].Update.ToolCallID)
	}
	checkUpdate(4, "agent_lifecycle", "pending")
	checkUpdate(5, "agent_lifecycle", "failed")
	checkUpdate(6, "hook_intervention", "completed")
	checkUpdate(7, "sandbox_update", "completed")
	checkUpdate(8, "sandbox_update", "completed")
	checkUpdate(9, "tool_call_update", "in_progress")
	checkUpdate(10, "run_status_update", "failed")

	var runEnd core.RunEndPayload
	if err := json.Unmarshal(got[10].Update.RawOutput, &runEnd); err != nil {
		t.Fatalf("run end raw output unmarshal: %v", err)
	}
	if runEnd.Reason != "budget_steps" || runEnd.UsedSteps != 2 {
		t.Fatalf("unexpected run end payload: %+v", runEnd)
	}
}

func TestTranslatorKeepsGenericRunErrorTitleWhenDetailMissing(t *testing.T) {
	var out bytes.Buffer
	conn := NewConn(strings.NewReader(""), &out)
	translate := NewTranslator(conn, "session-1")

	translate(acpEvent(t, core.EventRunError, core.RunErrorPayload{Error: "  "}))

	got := readACPUpdates(t, out.String())
	if len(got) != 1 {
		t.Fatalf("got %d notifications, want 1", len(got))
	}
	if got[0].Update.Type != "run_error" || got[0].Update.Status != "failed" {
		t.Fatalf("run error update = %#v", got[0].Update)
	}
	if got[0].Update.Title != "run error" {
		t.Fatalf("run error title = %q", got[0].Update.Title)
	}
}

func acpEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: typ, Payload: raw}
}

func readACPUpdates(t *testing.T, output string) []SessionUpdateParams {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	got := make([]SessionUpdateParams, 0, len(lines))
	for _, line := range lines {
		var notif Notification
		if err := json.Unmarshal([]byte(line), &notif); err != nil {
			t.Fatalf("notification unmarshal: %v", err)
		}
		if notif.Method != MethodSessionUpdate {
			t.Fatalf("notification method = %q, want %q", notif.Method, MethodSessionUpdate)
		}
		var params SessionUpdateParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			t.Fatalf("params unmarshal: %v", err)
		}
		got = append(got, params)
	}
	return got
}

func acpRawEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: typ, Payload: raw}
}

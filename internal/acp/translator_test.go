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

func acpEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: typ, Payload: raw}
}

func acpRawEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{Type: typ, Payload: raw}
}

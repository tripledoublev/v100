package core_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestSteerHookInjectsUnseenSteeringMessages(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(core.UserMsgPayload{Content: "pivot to storage", Source: "steer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   "run-1",
		EventID: "steer-1",
		Type:    core.EventUserMsg,
		Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}

	hook := core.SteerHook(tracePath)
	got := hook(core.LoopState{RunID: "run-1"})
	if got.Action != core.HookInjectMessage {
		t.Fatalf("Action = %v, want inject", got.Action)
	}
	if !strings.Contains(got.Message, "pivot to storage") || got.Reason != "external_steer" {
		t.Fatalf("unexpected hook result: %+v", got)
	}
	again := hook(core.LoopState{RunID: "run-1"})
	if again.Action != core.HookContinue {
		t.Fatalf("second Action = %v, want continue", again.Action)
	}
}

func TestSteerHookIgnoresNormalUserMessages(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(core.UserMsgPayload{Content: "normal prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   "run-1",
		EventID: "user-1",
		Type:    core.EventUserMsg,
		Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}

	got := core.SteerHook(tracePath)(core.LoopState{RunID: "run-1"})
	if got.Action != core.HookContinue {
		t.Fatalf("Action = %v, want continue", got.Action)
	}
}

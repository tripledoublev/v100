package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestTraceCmdRegistered(t *testing.T) {
	cmd := rootCmd()
	if child, _, err := cmd.Find([]string{"trace"}); err != nil || child == nil || child.Name() != "trace" {
		t.Fatalf("trace command not registered: child=%v err=%v", child, err)
	}
}

func TestHarnessTraceInspectRoundTrip(t *testing.T) {
	events := []core.Event{traceTestEvent(t, core.EventModelResp, core.ModelRespPayload{Text: "hello"})}
	data, err := marshalHarnessTrace("inspect", "run-1", events)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inspect.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readHarnessTrace(path, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != core.EventModelResp {
		t.Fatalf("events = %#v", got)
	}
}

func TestHarnessTraceMETRRoundTrip(t *testing.T) {
	events := []core.Event{traceTestEvent(t, core.EventToolResult, core.ToolResultPayload{CallID: "c1", Name: "fs_read", OK: true, Output: "data"})}
	data, err := marshalHarnessTrace("metr", "run-1", events)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "metr.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readHarnessTrace(path, "metr")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != core.EventToolResult {
		t.Fatalf("events = %#v", got)
	}
}

func TestReplayLoadsInspectTraceFile(t *testing.T) {
	data := []byte(`{"format":"inspect","events":[{"type":"model.response","payload":{"text":"from inspect"}}]}`)
	path := filepath.Join(t.TempDir(), "inspect.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	runID, events, err := loadReplayEvents(path, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	if runID != "inspect" {
		t.Fatalf("runID = %q, want inspect", runID)
	}
	if len(events) != 1 || events[0].Type != core.EventModelResp {
		t.Fatalf("events = %#v", events)
	}
}

func traceTestEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		TS:      time.Unix(1, 0).UTC(),
		RunID:   "run-1",
		StepID:  "step-1",
		EventID: "event-1",
		Type:    typ,
		Payload: b,
	}
}

package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestTraceWriteAndReadAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	tw, err := core.OpenTrace(path)
	if err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(core.UserMsgPayload{Content: "hello"})
	ev := core.Event{
		TS:      time.Now().UTC().Truncate(time.Millisecond),
		RunID:   "run1",
		StepID:  "step1",
		EventID: "ev1",
		Type:    core.EventUserMsg,
		Payload: payload,
	}

	if err := tw.Write(ev); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()

	events, err := core.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != core.EventUserMsg {
		t.Errorf("expected type %s, got %s", core.EventUserMsg, events[0].Type)
	}
	if events[0].RunID != "run1" {
		t.Errorf("expected run_id run1, got %s", events[0].RunID)
	}
}

func TestTraceMultipleEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	tw, err := core.OpenTrace(path)
	if err != nil {
		t.Fatal(err)
	}

	types := []core.EventType{core.EventRunStart, core.EventUserMsg, core.EventModelResp, core.EventRunEnd}
	for i, et := range types {
		p, _ := json.Marshal(map[string]string{"i": string(rune('0' + i))})
		if err := tw.Write(core.Event{
			TS:      time.Now().UTC(),
			RunID:   "run1",
			EventID: string(rune('a' + i)),
			Type:    et,
			Payload: p,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()

	events, err := core.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(types) {
		t.Fatalf("expected %d events, got %d", len(types), len(events))
	}
}

func TestReadAllMissingFile(t *testing.T) {
	_, err := core.ReadAll("/nonexistent/path/trace.jsonl")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestOpenTraceCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "trace.jsonl")

	tw, err := core.OpenTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected trace file to exist: %v", err)
	}
}

func TestReadAllReportsTrueOneBasedLineNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	content := strings.Join([]string{
		`{"ts":"2026-03-23T00:00:00Z","type":"user.msg","payload":{"content":"ok"}}`,
		`{"ts":"2026-03-23T00:00:01Z","type":"run.end","payload":{"reason":"completed"}}`,
		`CORRUPTED`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := core.ReadAll(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse line 3") {
		t.Fatalf("expected true 1-based line number in error, got %v", err)
	}
}

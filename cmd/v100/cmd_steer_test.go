package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestSteerCmdAppendsSteeringEvent(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		runID := "run-steer"
		runDir := filepath.Join("runs", runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return err
		}
		trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
		if err != nil {
			return err
		}
		if err := trace.Close(); err != nil {
			return err
		}

		cmd := steerCmd()
		if err := cmd.RunE(cmd, []string{runID, "pivot to tests"}); err != nil {
			return err
		}
		events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
		if err != nil {
			return err
		}
		if len(events) != 1 {
			t.Fatalf("len(events) = %d, want 1", len(events))
		}
		if events[0].Type != core.EventUserMsg {
			t.Fatalf("event type = %s, want user.message", events[0].Type)
		}
		var payload core.UserMsgPayload
		if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
			return err
		}
		if payload.Source != "steer" || payload.Content != "pivot to tests" {
			t.Fatalf("payload = %+v", payload)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestCollectRunsFiltersSortsAndReadsPrompt(t *testing.T) {
	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")

	mustWriteRunFixture(t, runRoot, "older-run", core.RunMeta{
		RunID:     "older-run",
		Provider:  "minimax",
		Model:     "MiniMax-M2.7",
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}, "older prompt", "completed")

	mustWriteRunFixture(t, runRoot, "newer-run", core.RunMeta{
		RunID:     "newer-run",
		Provider:  "gemini",
		Model:     "gemini-2.5-flash",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		Score:     "fail",
	}, "newer prompt", "error")

	mustWriteRunFixture(t, runRoot, "child-run", core.RunMeta{
		RunID:       "child-run",
		Provider:    "minimax",
		Model:       "MiniMax-M2.7",
		CreatedAt:   time.Now(),
		ParentRunID: "older-run",
	}, "child prompt", "completed")

	runs, err := collectRuns(runRoot, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].ID != "newer-run" || runs[1].ID != "older-run" {
		t.Fatalf("unexpected run order: %+v", runs)
	}
	if runs[0].Prompt != "newer prompt" {
		t.Fatalf("prompt = %q, want newer prompt", runs[0].Prompt)
	}

	failedRuns, err := collectRuns(runRoot, true, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(failedRuns) != 1 || failedRuns[0].ID != "newer-run" {
		t.Fatalf("failed filter returned %+v, want newer-run only", failedRuns)
	}

	providerRuns, err := collectRuns(runRoot, true, "minimax", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(providerRuns) != 2 {
		t.Fatalf("provider filter len = %d, want 2", len(providerRuns))
	}
}

func mustWriteRunFixture(t *testing.T, runRoot, runID string, meta core.RunMeta, prompt, endReason string) {
	t.Helper()
	dir := filepath.Join(runRoot, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	trace, err := core.OpenTrace(filepath.Join(dir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	userPayload, err := json.Marshal(core.UserMsgPayload{Content: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now(),
		RunID:   runID,
		EventID: "user",
		Type:    core.EventUserMsg,
		Payload: userPayload,
	}); err != nil {
		t.Fatal(err)
	}

	runEndPayload, err := json.Marshal(core.RunEndPayload{Reason: endReason})
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now(),
		RunID:   runID,
		EventID: "end",
		Type:    core.EventRunEnd,
		Payload: runEndPayload,
	}); err != nil {
		t.Fatal(err)
	}
}

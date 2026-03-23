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

	mustWriteRunFixture(t, runRoot, "20260323T010101-deadbeef", core.RunMeta{
		RunID:     "20260323T010101-deadbeef",
		Provider:  "minimax",
		Model:     "MiniMax-M2.7",
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}, "older prompt", "completed")

	mustWriteRunFixture(t, runRoot, "20260323T020202-feedbeef", core.RunMeta{
		RunID:     "20260323T020202-feedbeef",
		Provider:  "gemini",
		Model:     "gemini-2.5-flash",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		Score:     "fail",
	}, "newer prompt", "error")

	mustWriteRunFixture(t, runRoot, "20260323T030303-cafebabe", core.RunMeta{
		RunID:       "20260323T030303-cafebabe",
		Provider:    "minimax",
		Model:       "MiniMax-M2.7",
		CreatedAt:   time.Now(),
		ParentRunID: "20260323T010101-deadbeef",
	}, "child prompt", "completed")

	runs, err := collectRuns(runRoot, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs) = %d, want 2", len(runs))
	}
	if runs[0].ID != "20260323T020202-feedbeef" || runs[1].ID != "20260323T010101-deadbeef" {
		t.Fatalf("unexpected run order: %+v", runs)
	}
	if runs[0].Prompt != "newer prompt" {
		t.Fatalf("prompt = %q, want newer prompt", runs[0].Prompt)
	}

	failedRuns, err := collectRuns(runRoot, true, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(failedRuns) != 1 || failedRuns[0].ID != "20260323T020202-feedbeef" {
		t.Fatalf("failed filter returned %+v, want newer canonical run only", failedRuns)
	}

	providerRuns, err := collectRuns(runRoot, true, "minimax", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(providerRuns) != 2 {
		t.Fatalf("provider filter len = %d, want 2", len(providerRuns))
	}
}

func TestCollectRunsSkipsNonCanonicalDirectoriesByDefault(t *testing.T) {
	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")

	mustWriteRunFixture(t, runRoot, "20260323T040404-0badf00d", core.RunMeta{
		RunID:     "20260323T040404-0badf00d",
		Provider:  "codex",
		Model:     "gpt-5.4",
		CreatedAt: time.Now(),
	}, "canonical prompt", "completed")

	noiseDir := filepath.Join(runRoot, "agent-call_demo")
	if err := os.MkdirAll(noiseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noiseDir, "trace.jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noiseDir, "meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runs, err := collectRuns(runRoot, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != "20260323T040404-0badf00d" {
		t.Fatalf("default runs filter returned %+v, want canonical run only", runs)
	}

	allRuns, err := collectRuns(runRoot, true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(allRuns) != 2 {
		t.Fatalf("--all should include non-canonical directories, got %+v", allRuns)
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

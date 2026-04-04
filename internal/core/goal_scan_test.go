package core_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestScanWorkspaceGoalCandidatesFindsTODOs(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "internal", "core", "goal.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package core\n// TODO: tighten wake lifecycle tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		t.Fatalf("ScanWorkspaceGoalCandidates() error = %v", err)
	}

	candidate := findCandidateBySignal(candidates, "todo")
	if candidate == nil {
		t.Fatalf("expected TODO candidate, got %#v", candidates)
	}
	if !strings.Contains(candidate.Content, "tighten wake lifecycle tests") {
		t.Fatalf("candidate content = %q", candidate.Content)
	}
	if !strings.Contains(candidate.SourceAttribution, "internal/core/goal.go:2") {
		t.Fatalf("source attribution = %q", candidate.SourceAttribution)
	}
}

func TestScanWorkspaceGoalCandidatesFindsDirtyWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspace := t.TempDir()
	if out, err := exec.Command("git", "-C", workspace, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		t.Fatalf("ScanWorkspaceGoalCandidates() error = %v", err)
	}

	candidate := findCandidateBySignal(candidates, "dirty_worktree")
	if candidate == nil {
		t.Fatalf("expected dirty_worktree candidate, got %#v", candidates)
	}
	if !strings.Contains(candidate.SourceAttribution, "README.md") {
		t.Fatalf("source attribution = %q", candidate.SourceAttribution)
	}
}

func TestScanWorkspaceGoalCandidatesFindsFailureArtifacts(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "artifacts", "go-test.out")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "=== RUN   TestWakeGoalScanner\n--- FAIL: TestWakeGoalScanner (0.00s)\nFAIL\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		t.Fatalf("ScanWorkspaceGoalCandidates() error = %v", err)
	}

	candidate := findCandidateBySignal(candidates, "failure_artifact")
	if candidate == nil {
		t.Fatalf("expected failure_artifact candidate, got %#v", candidates)
	}
	if !strings.Contains(candidate.Content, "TestWakeGoalScanner") {
		t.Fatalf("candidate content = %q", candidate.Content)
	}
	if !strings.Contains(candidate.SourceAttribution, "artifacts/go-test.out") {
		t.Fatalf("source attribution = %q", candidate.SourceAttribution)
	}
}

func TestScanWorkspaceGoalCandidatesFindsFailedRuns(t *testing.T) {
	workspace := t.TempDir()
	runID := "20260404T120000-deadbeef"
	tracePath := filepath.Join(workspace, "runs", runID, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	writeGoalScanEvent(t, trace, core.Event{
		TS:    time.Now().UTC(),
		RunID: runID,
		Type:  core.EventRunError,
		Payload: mustGoalScanJSON(t, core.RunErrorPayload{
			Error: "max tool calls per step reached (4)",
		}),
	})
	writeGoalScanEvent(t, trace, core.Event{
		TS:    time.Now().UTC(),
		RunID: runID,
		Type:  core.EventRunEnd,
		Payload: mustGoalScanJSON(t, core.RunEndPayload{
			Reason: "error",
		}),
	})

	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		t.Fatalf("ScanWorkspaceGoalCandidates() error = %v", err)
	}

	candidate := findCandidateBySignal(candidates, "run_failure")
	if candidate == nil {
		t.Fatalf("expected run_failure candidate, got %#v", candidates)
	}
	if !strings.Contains(candidate.Content, runID) {
		t.Fatalf("candidate content = %q", candidate.Content)
	}
	if !strings.Contains(candidate.SourceAttribution, "max tool calls per step reached") {
		t.Fatalf("source attribution = %q", candidate.SourceAttribution)
	}
}

func findCandidateBySignal(candidates []core.GoalCandidate, signal string) *core.GoalCandidate {
	for i := range candidates {
		if candidates[i].Signal == signal {
			return &candidates[i]
		}
	}
	return nil
}

func mustGoalScanJSON(t *testing.T, payload any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", payload, err)
	}
	return data
}

func writeGoalScanEvent(t *testing.T, trace *core.TraceWriter, event core.Event) {
	t.Helper()
	if err := trace.Write(event); err != nil {
		t.Fatalf("trace.Write(%v): %v", event.Type, err)
	}
}

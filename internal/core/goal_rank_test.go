package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestRankGeneratedGoalsScoresAndOrdersMixedSet(t *testing.T) {
	now := time.Now()
	goals := []core.GeneratedGoal{
		{Content: "make better", CreatedAt: now},
		{Content: "Fix failing TestWakeGoalScanner in internal/core/goal_scan_test.go", CreatedAt: now.Add(time.Second)},
		{Content: "Investigate recent failed run 20260404T120000-deadbeef", CreatedAt: now.Add(2 * time.Second)},
	}

	got := core.RankGeneratedGoals(goals)
	if len(got) != 2 {
		t.Fatalf("len(RankGeneratedGoals) = %d, want 2: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "TestWakeGoalScanner") {
		t.Fatalf("top ranked goal = %q, want concrete test fix first", got[0].Content)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("scores not ordered descending: %d <= %d", got[0].Score, got[1].Score)
	}
	if len(got[0].Reasons) == 0 {
		t.Fatalf("expected score reasons on top goal: %#v", got[0])
	}
}

func TestRankGeneratedGoalsDedupesAfterScoring(t *testing.T) {
	goals := []core.GeneratedGoal{
		{Content: "Add tests for wake queue ranking"},
		{Content: " add tests for wake queue ranking "},
	}

	got := core.RankGeneratedGoals(goals)
	if len(got) != 1 {
		t.Fatalf("len(RankGeneratedGoals) = %d, want 1", len(got))
	}
	if got[0].Score == 0 {
		t.Fatalf("expected scored goal, got %#v", got[0])
	}
}

func TestRankGoalCandidatesPrioritizesFailureSignals(t *testing.T) {
	candidates := []core.GoalCandidate{
		{Content: "Address TODO in internal/core/wake.go: polish wording", Signal: "todo", SourceAttribution: "TODO signal"},
		{Content: "Investigate recent failed run 20260404T120000-deadbeef", Signal: "run_failure", SourceAttribution: "trace error"},
	}

	got := core.RankGoalCandidates(candidates)
	if got[0].Signal != "run_failure" {
		t.Fatalf("top candidate signal = %q, want run_failure", got[0].Signal)
	}
}

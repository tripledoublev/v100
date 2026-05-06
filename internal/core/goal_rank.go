package core

import (
	"sort"
	"strings"
)

const minimumGeneratedGoalScore = 35

// ScoreGeneratedGoal assigns a comparable, deterministic usefulness score to a generated goal.
func ScoreGeneratedGoal(goal GeneratedGoal) GeneratedGoal {
	content := strings.TrimSpace(goal.Content)
	goal.Content = content
	score, reasons := scoreGoalText(content)
	goal.Score = score
	goal.Reasons = reasons
	return goal
}

// RankGeneratedGoals scores, filters, deduplicates, and orders generated goals by usefulness.
func RankGeneratedGoals(goals []GeneratedGoal) []GeneratedGoal {
	seen := map[string]struct{}{}
	ranked := make([]GeneratedGoal, 0, len(goals))
	for _, goal := range goals {
		scored := ScoreGeneratedGoal(goal)
		key := strings.ToLower(scored.Content)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if scored.Score < minimumGeneratedGoalScore {
			continue
		}
		ranked = append(ranked, scored)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].CreatedAt.Before(ranked[j].CreatedAt)
	})
	return ranked
}

// RankGoalCandidates orders local signal candidates before they are shown to the model.
func RankGoalCandidates(candidates []GoalCandidate) []GoalCandidate {
	ranked := append([]GoalCandidate(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := scoreGoalCandidate(ranked[i])
		right := scoreGoalCandidate(ranked[j])
		if left != right {
			return left > right
		}
		return ranked[i].Content < ranked[j].Content
	})
	return ranked
}

func scoreGoalCandidate(candidate GoalCandidate) int {
	score, _ := scoreGoalText(candidate.Content)
	switch candidate.Signal {
	case "run_failure":
		score += 40
	case "failure_artifact":
		score += 20
	case "dirty_worktree":
		score += 12
	case "todo":
		score += 8
	}
	if candidate.SourceAttribution != "" {
		score += 5
	}
	return clampGoalScore(score)
}

func scoreGoalText(content string) (int, []string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0, nil
	}
	lower := strings.ToLower(content)
	if lower == "no actionable wake goal." || lower == "no actionable wake goal" {
		return 0, []string{"explicitly non-actionable"}
	}

	score := 30
	var reasons []string
	if containsAny(lower, "fix ", "add ", "implement ", "test ", "investigate ", "review ", "address ", "stabilize ", "tighten ", "document ") {
		score += 20
		reasons = append(reasons, "action verb")
	}
	if containsAny(lower, "test", "issue", "error", "failure", "bug", "regression", "todo") {
		score += 15
		reasons = append(reasons, "explicit engineering signal")
	}
	if strings.Contains(lower, "/") || strings.Contains(lower, ".go") || strings.Contains(lower, "#") {
		score += 10
		reasons = append(reasons, "specific artifact reference")
	}
	words := strings.Fields(content)
	switch {
	case len(words) >= 5 && len(words) <= 24:
		score += 10
		reasons = append(reasons, "bounded scope")
	case len(words) > 36:
		score -= 15
		reasons = append(reasons, "too broad")
	case len(words) < 3:
		score -= 20
		reasons = append(reasons, "underspecified")
	}
	if containsAny(lower, "improve things", "make better", "do more", "misc", "general cleanup") {
		score -= 30
		reasons = append(reasons, "vague wording")
	}
	return clampGoalScore(score), reasons
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func clampGoalScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

package core

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxGoalCandidates          = 12
	maxRunFailureCandidates    = 3
	maxArtifactCandidates      = 4
	maxTODOMarkerCandidates    = 5
	maxDirtyWorktreeCandidates = 4
	maxGoalScanFileBytes       = 256 * 1024
)

// GoalCandidate is a source-attributed candidate goal discovered from local workspace signals.
type GoalCandidate struct {
	Content           string `json:"content"`
	Signal            string `json:"signal"`
	SourceAttribution string `json:"source_attribution"`
}

// ScanWorkspaceGoalCandidates extracts candidate autonomous goals from bounded local workspace signals.
func ScanWorkspaceGoalCandidates(workspace string) ([]GoalCandidate, error) {
	var out []GoalCandidate
	seen := map[string]struct{}{}
	appendCandidate := func(candidate GoalCandidate) {
		if len(out) >= maxGoalCandidates {
			return
		}
		key := strings.ToLower(strings.TrimSpace(candidate.Content))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}

	runCandidates, err := scanGoalCandidatesFromRuns(workspace)
	if err != nil {
		return nil, err
	}
	for _, candidate := range runCandidates {
		appendCandidate(candidate)
	}

	artifactCandidates, err := scanGoalCandidatesFromArtifacts(workspace)
	if err != nil {
		return nil, err
	}
	for _, candidate := range artifactCandidates {
		appendCandidate(candidate)
	}

	todoCandidates, err := scanGoalCandidatesFromTODOs(workspace)
	if err != nil {
		return nil, err
	}
	for _, candidate := range todoCandidates {
		appendCandidate(candidate)
	}

	for _, candidate := range scanGoalCandidatesFromGitStatus(workspace) {
		appendCandidate(candidate)
	}

	return out, nil
}

func scanGoalCandidatesFromRuns(workspace string) ([]GoalCandidate, error) {
	runsDir := filepath.Join(workspace, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})

	candidates := make([]GoalCandidate, 0, maxRunFailureCandidates)
	for _, entry := range entries {
		if len(candidates) >= maxRunFailureCandidates {
			break
		}
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		tracePath := filepath.Join(runsDir, runID, "trace.jsonl")
		if _, err := os.Stat(tracePath); err != nil {
			continue
		}
		events, err := ReadAll(tracePath)
		if err != nil {
			continue
		}
		stats := ComputeStats(events)
		if isSuccessfulGoalScanEndReason(stats.EndReason) {
			continue
		}
		errs := runErrorMessages(events)
		classification := ClassifyRun(events)
		if stats.EndReason == "" && len(errs) == 0 {
			continue
		}
		detail := fmt.Sprintf("run ended with reason=%s", stats.EndReason)
		if len(errs) > 0 {
			detail = errs[0]
		} else if len(classification.Evidence) > 0 {
			detail = classification.Evidence[0]
		}
		candidates = append(candidates, GoalCandidate{
			Content:           fmt.Sprintf("Investigate recent failed run %s", runID),
			Signal:            "run_failure",
			SourceAttribution: fmt.Sprintf("runs/%s/trace.jsonl indicates %s", runID, clipGoalScanText(detail)),
		})
	}
	return candidates, nil
}

func scanGoalCandidatesFromArtifacts(workspace string) ([]GoalCandidate, error) {
	var candidates []GoalCandidate
	err := filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if shouldSkipGoalScanPath(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !looksLikeArtifactFile(rel) {
			return nil
		}
		content, ok, err := readGoalScanFile(path)
		if err != nil || !ok {
			return err
		}
		candidate, ok := extractArtifactCandidate(rel, content)
		if !ok {
			return nil
		}
		candidates = append(candidates, candidate)
		if len(candidates) >= maxArtifactCandidates {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, err
	}
	return candidates, nil
}

func scanGoalCandidatesFromTODOs(workspace string) ([]GoalCandidate, error) {
	var candidates []GoalCandidate
	err := filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if shouldSkipGoalScanPath(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		content, ok, err := readGoalScanFile(path)
		if err != nil || !ok {
			return err
		}
		isTODOFile := looksLikeTODOFile(rel)
		for idx, line := range strings.Split(content, "\n") {
			snippet := extractTODOCandidateLine(line, isTODOFile)
			if snippet == "" {
				continue
			}
			candidates = append(candidates, GoalCandidate{
				Content:           fmt.Sprintf("Address TODO in %s: %s", rel, snippet),
				Signal:            "todo",
				SourceAttribution: fmt.Sprintf("TODO signal at %s:%d", rel, idx+1),
			})
			if len(candidates) >= maxTODOMarkerCandidates {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, err
	}
	return candidates, nil
}

func scanGoalCandidatesFromGitStatus(workspace string) []GoalCandidate {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", workspace, "status", "--porcelain=v1", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var candidates []GoalCandidate
	for _, raw := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		status, path, ok := parseGoalScanGitStatusLine(line)
		if !ok {
			continue
		}
		content := fmt.Sprintf("Review and finish local changes in %s", path)
		if status == "??" {
			content = fmt.Sprintf("Decide whether to integrate new file %s into the workspace", path)
		}
		candidates = append(candidates, GoalCandidate{
			Content:           content,
			Signal:            "dirty_worktree",
			SourceAttribution: fmt.Sprintf("git status reports %s on %s", status, path),
		})
		if len(candidates) >= maxDirtyWorktreeCandidates {
			break
		}
	}
	return candidates
}

func shouldSkipGoalScanPath(rel string, d fs.DirEntry) bool {
	top := strings.Split(rel, "/")[0]
	switch top {
	case ".git", "runs", "node_modules", "vendor":
		return true
	}
	if strings.HasPrefix(top, ".gocache") || strings.HasPrefix(top, ".gomodcache") {
		return true
	}
	return false
}

func isSuccessfulGoalScanEndReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "completed", "wake_cycle_complete", "prompt_exit", "user_exit":
		return true
	default:
		return false
	}
}

func looksLikeArtifactFile(rel string) bool {
	lower := strings.ToLower(rel)
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.Contains(lower, "/artifacts/"),
		strings.Contains(lower, "/reports/"),
		strings.Contains(lower, "/analysis/"),
		strings.Contains(base, "fail"),
		strings.Contains(base, "failure"),
		strings.Contains(base, "regression"),
		strings.Contains(base, "report"),
		strings.Contains(base, "analysis"):
		return true
	default:
		return strings.HasSuffix(base, ".log") || strings.HasSuffix(base, ".out")
	}
}

func looksLikeTODOFile(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return strings.Contains(base, "todo")
}

func readGoalScanFile(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if info.Size() > maxGoalScanFileBytes {
		return "", false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return "", false, nil
	}
	return string(data), true, nil
}

func extractArtifactCandidate(rel, content string) (GoalCandidate, bool) {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "--- FAIL:") {
			testName := clipGoalScanText(strings.TrimSpace(strings.TrimPrefix(line, "--- FAIL:")))
			return GoalCandidate{
				Content:           fmt.Sprintf("Investigate failing test %s", testName),
				Signal:            "failure_artifact",
				SourceAttribution: fmt.Sprintf("%s contains %q", rel, clipGoalScanText(line)),
			}, true
		}
	}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "panic:"),
			strings.Contains(lower, "regression"),
			strings.Contains(lower, "flaky"),
			strings.Contains(lower, "fatal:"),
			strings.Contains(lower, "assertion failed"),
			strings.Contains(lower, "test failed"),
			strings.Contains(lower, "fail\t"),
			strings.HasPrefix(lower, "fail "):
			return GoalCandidate{
				Content:           fmt.Sprintf("Investigate failure artifact %s", rel),
				Signal:            "analysis_artifact",
				SourceAttribution: fmt.Sprintf("%s notes %q", rel, clipGoalScanText(line)),
			}, true
		}
	}
	return GoalCandidate{}, false
}

func extractTODOCandidateLine(line string, todoFile bool) string {
	markers := []string{"TODO", "FIXME", "XXX"}
	upper := strings.ToUpper(line)
	for _, marker := range markers {
		idx := strings.Index(upper, marker)
		if idx < 0 {
			continue
		}
		return cleanGoalScanSnippet(line[idx+len(marker):])
	}
	if todoFile {
		return cleanGoalScanSnippet(line)
	}
	return ""
}

func cleanGoalScanSnippet(raw string) string {
	line := strings.TrimSpace(raw)
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, "#") {
		return ""
	}
	line = strings.TrimLeft(line, ": -*0123456789.[]()")
	line = strings.TrimSpace(line)
	line = strings.Join(strings.Fields(line), " ")
	return clipGoalScanText(line)
}

func parseGoalScanGitStatusLine(line string) (status string, path string, ok bool) {
	if len(line) < 4 {
		return "", "", false
	}
	status = strings.TrimSpace(line[:2])
	path = strings.TrimSpace(line[3:])
	if path == "" {
		return "", "", false
	}
	if idx := strings.LastIndex(path, " -> "); idx >= 0 {
		path = path[idx+4:]
	}
	path = strings.Trim(path, "\"")
	path = filepath.ToSlash(path)
	return status, path, true
}

func clipGoalScanText(text string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if len(text) <= 120 {
		return text
	}
	return strings.TrimSpace(text[:117]) + "..."
}

package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"fmt"
	"testing"
	"time"
)

func TestScoreToNumeric(t *testing.T) {
	tests := []struct {
		score string
		want  float64
	}{
		{"pass", 1},
		{"PASS", 1},
		{"Pass", 1},
		{"fail", 0},
		{"FAIL", 0},
		{"partial", 0.5},
		{"PARTIAL", 0.5},
		{"", -1},
		{"unknown", -1},
		{" pass ", 1},
	}

	for _, tt := range tests {
		got := ScoreToNumeric(tt.score)
		if got != tt.want {
			t.Errorf("ScoreToNumeric(%q) = %.1f, want %.1f", tt.score, got, tt.want)
		}
	}
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		values []float64
		want   string
	}{
		{[]float64{1, 0.5, 0, -1}, "█▄░·"},
		{[]float64{1, 1, 1}, "███"},
		{[]float64{0, 0, 0}, "░░░"},
		{[]float64{}, ""},
		{[]float64{-1}, "·"},
	}

	for _, tt := range tests {
		got := Sparkline(tt.values)
		if got != tt.want {
			t.Errorf("Sparkline(%v) = %q, want %q", tt.values, got, tt.want)
		}
	}
}

func TestLoadHistory(t *testing.T) {
	// Create temp runs directory with test data
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create two runs for bench "mybench"
	for i, score := range []string{"pass", "fail"} {
		runDir := filepath.Join(runsDir, fmt.Sprintf("run%d", i))
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}

		meta := map[string]interface{}{
			"run_id":     fmt.Sprintf("run%d", i),
			"name":       "mybench",
			"provider":   "gemini",
			"model":      "gemini-2.5-pro",
			"score":      score,
			"created_at": time.Date(2025, 4, 18, 10, i, 0, 0, time.UTC),
			"tags": map[string]string{
				"experiment": "mybench",
				"variant":    "default",
			},
		}
		b, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(runDir, "meta.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a run for a different bench
	otherDir := filepath.Join(runsDir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]interface{}{
		"run_id":     "other1",
		"name":       "otherbench",
		"provider":   "openai",
		"model":      "gpt-4",
		"score":      "pass",
		"created_at": time.Date(2025, 4, 18, 12, 0, 0, 0, time.UTC),
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(otherDir, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// Load history for "mybench"
	entries, err := LoadHistory(runsDir, "mybench")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Should be sorted by CreatedAt ascending
	if entries[0].Score != "pass" {
		t.Errorf("first entry should be pass, got %s", entries[0].Score)
	}
	if entries[1].Score != "fail" {
		t.Errorf("second entry should be fail, got %s", entries[1].Score)
	}
}

func TestLoadHistoryEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	entries, err := LoadHistory(tmpDir, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestLoadHistoryByTag(t *testing.T) {
	tmpDir := t.TempDir()
	runsDir := filepath.Join(tmpDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Run with no name but matching experiment tag
	runDir := filepath.Join(runsDir, "tagrun")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]interface{}{
		"run_id":     "tagrun",
		"name":       "",
		"provider":   "gemini",
		"model":      "gemini-2.5-pro",
		"score":      "pass",
		"created_at": time.Date(2025, 4, 18, 10, 0, 0, 0, time.UTC),
		"tags": map[string]string{
			"experiment": "tagged_bench",
		},
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadHistory(runsDir, "tagged_bench")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry via tag match, got %d", len(entries))
	}
	if entries[0].RunID != "tagrun" {
		t.Errorf("expected tagrun, got %s", entries[0].RunID)
	}
}

func TestFormatHistoryTable(t *testing.T) {
	entries := []HistoryEntry{
		{
			RunID:     "run1",
			Provider:  "gemini",
			Model:     "gemini-2.5-pro",
			Variant:   "default",
			Score:     "pass",
			CreatedAt: time.Date(2025, 4, 18, 10, 0, 0, 0, time.UTC),
		},
	}
	output := FormatHistoryTable(entries)
	if !contains(output, "run1") || !contains(output, "pass") {
		t.Errorf("table output missing expected data:\n%s", output)
	}

	// Empty
	output = FormatHistoryTable(nil)
	if output != "No history found." {
		t.Errorf("expected empty message, got: %s", output)
	}
}

func TestFormatTrendSummary(t *testing.T) {
	entries := []HistoryEntry{
		{
			Name:      "mybench",
			RunID:     "r1",
			Provider:  "gemini",
			Model:     "pro",
			Score:     "pass",
			CreatedAt: time.Date(2025, 4, 18, 10, 0, 0, 0, time.UTC),
		},
		{
			Name:      "mybench",
			RunID:     "r2",
			Provider:  "gemini",
			Model:     "pro",
			Score:     "fail",
			CreatedAt: time.Date(2025, 4, 18, 11, 0, 0, 0, time.UTC),
		},
		{
			Name:      "mybench",
			RunID:     "r3",
			Provider:  "gemini",
			Model:     "pro",
			Score:     "pass",
			CreatedAt: time.Date(2025, 4, 18, 12, 0, 0, 0, time.UTC),
		},
	}
	output := FormatTrendSummary(entries)

	if !contains(output, "mybench") {
		t.Error("trend summary missing bench name")
	}
	if !contains(output, "Pass Rate: 67%") {
		t.Errorf("trend summary missing pass rate:\n%s", output)
	}
	if !contains(output, "█░█") {
		t.Errorf("trend summary missing sparkline:\n%s", output)
	}

	// Empty
	output = FormatTrendSummary(nil)
	if output != "No history found." {
		t.Errorf("expected empty message, got: %s", output)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

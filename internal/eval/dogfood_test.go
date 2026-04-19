package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverQuests(t *testing.T) {
	// Create temp dogfood directory with test bench files
	tmpDir := t.TempDir()
	dogfoodDir := filepath.Join(tmpDir, "dogfood")
	if err := os.MkdirAll(dogfoodDir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := []string{
		"smoke.bench.toml",
		"reliable.bench.toml",
		"verify_test.toml",
		"README.md", // should be skipped
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dogfoodDir, f), []byte("name = \"test\""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	quests, err := DiscoverQuests(dogfoodDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(quests) != 3 {
		t.Fatalf("expected 3 quests, got %d", len(quests))
	}

	// README should be excluded
	names := make(map[string]bool)
	for _, q := range quests {
		names[q.Name] = true
	}
	if names["README"] {
		t.Error("README should not be a quest")
	}
	if !names["smoke.bench"] {
		t.Error("smoke.bench should be a quest")
	}
}

func TestDiscoverQuestsEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	quests, err := DiscoverQuests(filepath.Join(tmpDir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	_ = quests
}

func TestFilterQuests(t *testing.T) {
	quests := []DogfoodQuest{
		{Name: "smoke", File: "dogfood/smoke.bench.toml"},
		{Name: "reliable", File: "dogfood/reliable.bench.toml"},
		{Name: "verify", File: "dogfood/verify_test.toml"},
	}

	// No filter → all
	filtered := FilterQuests(quests, nil)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 with no filter, got %d", len(filtered))
	}

	// Filter to one
	filtered = FilterQuests(quests, []string{"smoke"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 for smoke filter, got %d", len(filtered))
	}
	if filtered[0].Name != "smoke" {
		t.Errorf("expected smoke, got %s", filtered[0].Name)
	}

	// Filter by filename
	filtered = FilterQuests(quests, []string{"verify_test.toml"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 for filename filter, got %d", len(filtered))
	}

	// Nonexistent → empty
	filtered = FilterQuests(quests, []string{"nonexistent"})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 for nonexistent filter, got %d", len(filtered))
	}
}

func TestFormatReport(t *testing.T) {
	report := DogfoodReport{
		Total: 3,
		Results: []DogfoodResult{
			{Quest: DogfoodQuest{Name: "smoke"}, Score: "pass"},
			{Quest: DogfoodQuest{Name: "reliable"}, Score: "fail"},
			{Quest: DogfoodQuest{Name: "verify"}, Score: "skipped"},
		},
	}
	report.Passed = 1
	report.Failed = 1
	report.Skipped = 1

	output := FormatReport(report)
	if !containsSubstr(output, "smoke") || !containsSubstr(output, "PASS") {
		t.Errorf("report missing quest data:\n%s", output)
	}
	if !containsSubstr(output, "Total: 3") {
		t.Errorf("report missing totals:\n%s", output)
	}
}

func TestDetectRegressions(t *testing.T) {
	previous := []DogfoodResult{
		{Quest: DogfoodQuest{Name: "a"}, Score: "pass"},
		{Quest: DogfoodQuest{Name: "b"}, Score: "pass"},
		{Quest: DogfoodQuest{Name: "c"}, Score: "fail"},
	}

	current := []DogfoodResult{
		{Quest: DogfoodQuest{Name: "a"}, Score: "fail"},   // regression
		{Quest: DogfoodQuest{Name: "b"}, Score: "pass"},   // stable
		{Quest: DogfoodQuest{Name: "c"}, Score: "fail"},   // was already failing
	}

	regressions := DetectRegressions(current, previous)
	if len(regressions) != 1 {
		t.Fatalf("expected 1 regression, got %d: %v", len(regressions), regressions)
	}
	if regressions[0] != "a" {
		t.Errorf("expected regression on 'a', got %s", regressions[0])
	}
}

func TestDetectRegressionsNone(t *testing.T) {
	previous := []DogfoodResult{
		{Quest: DogfoodQuest{Name: "a"}, Score: "pass"},
	}

	current := []DogfoodResult{
		{Quest: DogfoodQuest{Name: "a"}, Score: "pass"},
	}

	regressions := DetectRegressions(current, previous)
	if len(regressions) != 0 {
		t.Fatalf("expected 0 regressions, got %d", len(regressions))
	}
}

func TestShortCommit(t *testing.T) {
	if shortCommit("abcdef1234567890") != "abcdef12" {
		t.Errorf("expected 8-char short hash")
	}
	if shortCommit("abc") != "abc" {
		t.Errorf("expected short hash unchanged")
	}
	if shortCommit("") != "" {
		t.Errorf("expected empty for empty input")
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

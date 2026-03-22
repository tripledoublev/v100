package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestRunMetaMarshalUnmarshalWithGeneratedGoals(t *testing.T) {
	now := time.Now().UTC()
	meta := core.RunMeta{
		RunID:             "test-run-123",
		Name:              "test run",
		Provider:          "gemini",
		Model:             "gemini-2.0-flash",
		SourceWorkspace:   ".",
		CreatedAt:         now,
		GeneratedGoals: []core.GeneratedGoal{
			{
				ID:        "goal-1",
				Content:   "Implement authentication system",
				StepID:    "step-1",
				CreatedAt: now,
			},
			{
				ID:        "goal-2",
				Content:   "Add user database schema",
				StepID:    "step-2",
				CreatedAt: now.Add(1 * time.Second),
			},
		},
	}

	// Test marshaling
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Test unmarshaling
	var recovered core.RunMeta
	if err := json.Unmarshal(b, &recovered); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify fields
	if recovered.RunID != meta.RunID {
		t.Errorf("RunID mismatch: got %q, want %q", recovered.RunID, meta.RunID)
	}
	if recovered.Name != meta.Name {
		t.Errorf("Name mismatch: got %q, want %q", recovered.Name, meta.Name)
	}
	if len(recovered.GeneratedGoals) != len(meta.GeneratedGoals) {
		t.Errorf("GeneratedGoals length mismatch: got %d, want %d", len(recovered.GeneratedGoals), len(meta.GeneratedGoals))
	}
	for i, goal := range recovered.GeneratedGoals {
		if goal.ID != meta.GeneratedGoals[i].ID {
			t.Errorf("Goal %d ID mismatch: got %q, want %q", i, goal.ID, meta.GeneratedGoals[i].ID)
		}
		if goal.Content != meta.GeneratedGoals[i].Content {
			t.Errorf("Goal %d Content mismatch: got %q, want %q", i, goal.Content, meta.GeneratedGoals[i].Content)
		}
	}
}

func TestWriteReadMetaPreservesGeneratedGoals(t *testing.T) {
	tmpDir := t.TempDir()

	now := time.Now().UTC()
	originalMeta := core.RunMeta{
		RunID:           "test-run-456",
		Provider:        "anthropic",
		Model:           "claude-3-5-sonnet",
		CreatedAt:       now,
		SourceWorkspace: ".",
		GeneratedGoals: []core.GeneratedGoal{
			{
				ID:        "autonomous-goal-1",
				Content:   "Improve code quality metrics",
				CreatedAt: now,
			},
		},
	}

	// Write meta
	if err := core.WriteMeta(tmpDir, originalMeta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Verify file was created
	metaFile := filepath.Join(tmpDir, "meta.json")
	if _, err := os.Stat(metaFile); err != nil {
		t.Fatalf("meta.json not created: %v", err)
	}

	// Read meta back
	readMeta, err := core.ReadMeta(tmpDir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	// Verify goals were preserved
	if len(readMeta.GeneratedGoals) != 1 {
		t.Fatalf("GeneratedGoals count: got %d, want 1", len(readMeta.GeneratedGoals))
	}
	if readMeta.GeneratedGoals[0].ID != "autonomous-goal-1" {
		t.Errorf("Goal ID: got %q, want %q", readMeta.GeneratedGoals[0].ID, "autonomous-goal-1")
	}
	if readMeta.GeneratedGoals[0].Content != "Improve code quality metrics" {
		t.Errorf("Goal Content: got %q, want %q", readMeta.GeneratedGoals[0].Content, "Improve code quality metrics")
	}
}

func TestRunMetaBackwardCompatibilityWithoutGoals(t *testing.T) {
	// Test that RunMeta without GeneratedGoals still works
	meta := core.RunMeta{
		RunID:           "old-run",
		Provider:        "openai",
		Model:           "gpt-4",
		CreatedAt:       time.Now().UTC(),
		SourceWorkspace: ".",
		// GeneratedGoals intentionally omitted
	}

	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var recovered core.RunMeta
	if err := json.Unmarshal(b, &recovered); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// GeneratedGoals should be empty (nil or zero-length slice both acceptable as JSON zero value)
	if len(recovered.GeneratedGoals) != 0 {
		t.Errorf("GeneratedGoals should be empty: got %d", len(recovered.GeneratedGoals))
	}
}

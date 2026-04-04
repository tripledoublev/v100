package core

import (
	"testing"
	"time"
)

func TestCategoryBoost(t *testing.T) {
	if b := categoryBoost("constraint", "anything"); b != 4 {
		t.Errorf("constraint boost = %d, want 4", b)
	}
	if b := categoryBoost("preference", "what style should I use"); b != 2 {
		t.Errorf("preference boost for style query = %d, want 2", b)
	}
	if b := categoryBoost("preference", "hello world"); b != 0 {
		t.Errorf("preference boost for unrelated query = %d, want 0", b)
	}
	if b := categoryBoost("fact", "anything"); b != 0 {
		t.Errorf("fact boost = %d, want 0", b)
	}
	if b := categoryBoost("", "anything"); b != 0 {
		t.Errorf("empty category boost = %d, want 0", b)
	}
}

func TestSelectMemoryReferenceEntries_ExpiryFiltering(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	entries := []memoryReferenceEntry{
		{Content: "expired note", ExpiresAt: &past},
		{Content: "valid constraint about postgres", Category: "constraint"},
		{Content: "future note about postgres", ExpiresAt: &future},
	}
	results := selectMemoryReferenceEntries(entries, "postgres", true, 10)
	for _, r := range results {
		if r.Content == "expired note" {
			t.Error("expired entry should have been filtered out")
		}
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestSelectMemoryReferenceEntries_ConstraintBoost(t *testing.T) {
	entries := []memoryReferenceEntry{
		{Content: "some general fact about databases", Category: "fact"},
		{Content: "always use snake_case", Category: "constraint"},
	}
	results := selectMemoryReferenceEntries(entries, "databases", true, 10)
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Constraint should be boosted higher even though query matches "fact" entry better
	found := false
	for _, r := range results {
		if r.Category == "constraint" && r.Confidence >= 4 {
			found = true
		}
	}
	if !found {
		t.Error("constraint entry should have confidence >= 4 from category boost")
	}
}

func TestSelectMemoryReferenceEntries_FallbackExcludesExpiredEntries(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	entries := []memoryReferenceEntry{
		{Content: "expired temporary note", ExpiresAt: &past},
		{Content: "active temporary note", ExpiresAt: &future},
	}

	results := selectMemoryReferenceEntries(entries, "remember what we said earlier", false, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "active temporary note" {
		t.Fatalf("unexpected fallback result: %+v", results)
	}
}

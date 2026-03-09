package memory

import (
	"math"
	"path/filepath"
	"testing"
)

func TestVectorStore_AddAndLoad(t *testing.T) {
	runID := "test-run-mem"
	tmpDir := t.TempDir()

	// Override default path logic for testing
	s := &VectorStore{
		runID:   runID,
		runPath: filepath.Join(tmpDir, "blackboard.vectors.json"),
		items:   []MemoryItem{},
	}

	item := MemoryItem{
		ID:        "item-1",
		Content:   "hello world",
		Embedding: []float32{1.0, 0.0, 0.0},
	}

	if err := s.Add(item); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// New store instance should load the same data
	s2 := &VectorStore{
		runID:   runID,
		runPath: filepath.Join(tmpDir, "blackboard.vectors.json"),
	}
	if err := s2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(s2.items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(s2.items))
	}
	if s2.items[0].Content != "hello world" {
		t.Errorf("Content mismatch: %s", s2.items[0].Content)
	}
}

func TestVectorStore_Search(t *testing.T) {
	s := &VectorStore{
		items: []MemoryItem{
			{ID: "1", Content: "A", Embedding: []float32{1.0, 0.0}},
			{ID: "2", Content: "B", Embedding: []float32{0.0, 1.0}},
			{ID: "3", Content: "C", Embedding: []float32{0.7, 0.7}},
		},
	}

	// Query closest to "C" (diagonal)
	query := []float32{0.5, 0.5}
	results := s.Search(query, 2)

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	if results[0].Item.ID != "3" {
		t.Errorf("Expected top result ID '3', got %s (score %f)", results[0].Item.ID, results[0].Score)
	}
}

func TestVectorStore_SearchEdgeCases(t *testing.T) {
	s := &VectorStore{
		items: []MemoryItem{
			{ID: "1", Embedding: []float32{1.0, 0.0}},
		},
	}

	// Case 1: k <= 0 (Should not panic)
	results := s.Search([]float32{1.0, 0.0}, 0)
	if len(results) != 0 {
		t.Errorf("Expected 0 results for k=0, got %d", len(results))
	}

	results = s.Search([]float32{1.0, 0.0}, -1)
	if len(results) != 0 {
		t.Errorf("Expected 0 results for k=-1, got %d", len(results))
	}

	// Case 2: Mismatched embedding dimensions
	results = s.Search([]float32{1.0, 0.0, 0.0}, 1)
	if results[0].Score != 0 {
		t.Errorf("Expected score 0 for mismatched dims, got %f", results[0].Score)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0}, []float32{1, 0}, 1.0},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0.0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"diagonal", []float32{1, 0}, []float32{1, 1}, float32(1.0 / math.Sqrt(2))},
		{"empty", []float32{}, []float32{}, 0.0},
		{"mismatch", []float32{1}, []float32{1, 0}, 0.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cosineSimilarity(tc.a, tc.b)
			if math.Abs(float64(got-tc.want)) > 1e-6 {
				t.Errorf("got %f, want %f", got, tc.want)
			}
		})
	}
}

package memory

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MemoryItem is a single record in the vectorized blackboard.
type MemoryItem struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding,omitempty"`
	Metadata  Metadata  `json:"metadata"`
	TS        time.Time `json:"ts"`
}

// Metadata holds extra context for a memory record.
type Metadata struct {
	AgentRole string            `json:"agent_role,omitempty"`
	StepID    string            `json:"step_id,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
}

// SearchResult pairs an item with its relevance score.
type SearchResult struct {
	Item  MemoryItem `json:"item"`
	Score float32    `json:"score"`
}

// VectorStore is a simple, local vector database for agent runs.
type VectorStore struct {
	runID   string
	runPath string
	items   []MemoryItem
}

// NewVectorStore initializes a store for a specific run.
func NewVectorStore(runID string) *VectorStore {
	return &VectorStore{
		runID:   runID,
		runPath: filepath.Join("runs", runID, "blackboard.vectors.json"),
		items:   []MemoryItem{},
	}
}

// Load reads existing vectors from disk.
func (s *VectorStore) Load() error {
	data, err := os.ReadFile(s.runPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.items)
}

// Save persists the current state to disk.
func (s *VectorStore) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.runPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.runPath, data, 0644)
}

// Add appends a new item and saves.
func (s *VectorStore) Add(item MemoryItem) error {
	s.items = append(s.items, item)
	return s.Save()
}

// Search returns top-k items sorted by cosine similarity to the query embedding.
func (s *VectorStore) Search(query []float32, k int) []SearchResult {
	if len(s.items) == 0 {
		return nil
	}

	results := make([]SearchResult, 0, len(s.items))
	for _, item := range s.items {
		score := cosineSimilarity(query, item.Embedding)
		results = append(results, SearchResult{Item: item, Score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > k {
		results = results[:k]
	}
	return results
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

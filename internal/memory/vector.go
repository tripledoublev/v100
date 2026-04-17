package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// MemoryItem is a single record in the vectorized blackboard.
type MemoryItem struct {
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	Category  string     `json:"category,omitempty"` // fact, preference, constraint, note
	Embedding []float32  `json:"embedding,omitempty"`
	Metadata  Metadata   `json:"metadata"`
	TS        time.Time  `json:"ts"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
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

// NewWorkspaceVectorStore initializes a store scoped to a workspace directory.
func NewWorkspaceVectorStore(workspaceDir string) *VectorStore {
	return &VectorStore{
		runPath: filepath.Join(workspaceDir, "blackboard.vectors.json"),
		items:   []MemoryItem{},
	}
}

// NewNamedVectorStore initializes a named store in the given directory.
// The backing file will be <name>.vectors.json.
func NewNamedVectorStore(workspaceDir, name string) *VectorStore {
	return &VectorStore{
		runPath: filepath.Join(workspaceDir, name+".vectors.json"),
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

// HasTag reports whether any item in the store has the given tag key=value pair.
// Used for deduplication at indexing time.
func (s *VectorStore) HasTag(key, value string) bool {
	for _, item := range s.items {
		if item.Metadata.Tags != nil && item.Metadata.Tags[key] == value {
			return true
		}
	}
	return false
}

// Add appends a new item and saves.
func (s *VectorStore) Add(item MemoryItem) error {
	s.items = append(s.items, item)
	return s.Save()
}

// Expired reports whether the item should no longer be surfaced.
func (m MemoryItem) Expired(now time.Time) bool {
	return m.ExpiresAt != nil && !m.ExpiresAt.After(now)
}

// Items returns a copy of the currently loaded items.
func (s *VectorStore) Items() []MemoryItem {
	now := time.Now()
	out := make([]MemoryItem, 0, len(s.items))
	for _, item := range s.items {
		if item.Expired(now) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// Remove deletes an item by ID and saves.
func (s *VectorStore) Remove(id string) error {
	kept := s.items[:0]
	found := false
	for _, item := range s.items {
		if item.ID == id {
			found = true
			continue
		}
		kept = append(kept, item)
	}
	if !found {
		return fmt.Errorf("memory entry %q not found", id)
	}
	s.items = append([]MemoryItem(nil), kept...)
	return s.Save()
}

// Prune removes expired items and saves if any were removed.
func (s *VectorStore) Prune() int {
	now := time.Now()
	kept := s.items[:0]
	removed := 0
	for _, item := range s.items {
		if item.Expired(now) {
			removed++
			continue
		}
		kept = append(kept, item)
	}
	if removed > 0 {
		s.items = append([]MemoryItem(nil), kept...)
		_ = s.Save()
	}
	return removed
}

// Search returns top-k items sorted by cosine similarity to the query embedding.
func (s *VectorStore) Search(query []float32, k int) []SearchResult {
	if k <= 0 || len(s.items) == 0 {
		return nil
	}

	results := make([]SearchResult, 0, len(s.items))
	now := time.Now()
	for _, item := range s.items {
		if item.Expired(now) {
			continue
		}
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

package memory

import (
	"context"
	"encoding/json"
	"errors"
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

const (
	DefaultVectorStoreTTL              = 7 * 24 * time.Hour
	DefaultVectorStoreMaxItemsPerScope = 1000
	DefaultVectorStoreMaxBytes         = 4 * 1024 * 1024
	duplicateEmbeddingThreshold        = 0.999999
)

var ErrDuplicateEmbedding = errors.New("duplicate memory embedding")

// VectorStoreOptions bounds a vector store's lifecycle and disk footprint.
type VectorStoreOptions struct {
	DefaultTTL       time.Duration `json:"default_ttl"`
	MaxItemsPerScope int           `json:"max_items_per_scope"`
	MaxStoreBytes    int64         `json:"max_store_bytes"`
}

// DefaultVectorStoreOptions returns the bounded lifecycle defaults.
func DefaultVectorStoreOptions() VectorStoreOptions {
	return VectorStoreOptions{
		DefaultTTL:       DefaultVectorStoreTTL,
		MaxItemsPerScope: DefaultVectorStoreMaxItemsPerScope,
		MaxStoreBytes:    DefaultVectorStoreMaxBytes,
	}
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
	opts    VectorStoreOptions
}

// NewVectorStore initializes a store for a specific run.
func NewVectorStore(runID string) *VectorStore {
	return &VectorStore{
		runID:   runID,
		runPath: filepath.Join("runs", runID, "blackboard.vectors.json"),
		items:   []MemoryItem{},
		opts:    DefaultVectorStoreOptions(),
	}
}

// NewWorkspaceVectorStore initializes a store scoped to a workspace directory.
func NewWorkspaceVectorStore(workspaceDir string) *VectorStore {
	return &VectorStore{
		runPath: filepath.Join(workspaceDir, "blackboard.vectors.json"),
		items:   []MemoryItem{},
		opts:    DefaultVectorStoreOptions(),
	}
}

// NewNamedVectorStore initializes a named store in the given directory.
// The backing file will be <name>.vectors.json.
func NewNamedVectorStore(workspaceDir, name string) *VectorStore {
	return &VectorStore{
		runPath: filepath.Join(workspaceDir, name+".vectors.json"),
		items:   []MemoryItem{},
		opts:    DefaultVectorStoreOptions(),
	}
}

// WithOptions returns a copy of the store using normalized lifecycle options.
func (s *VectorStore) WithOptions(opts VectorStoreOptions) *VectorStore {
	cp := *s
	cp.opts = normalizeOptions(opts)
	return &cp
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
	if err := json.Unmarshal(data, &s.items); err != nil {
		return err
	}
	s.Prune()
	return nil
}

// Save persists the current state to disk.
func (s *VectorStore) Save() error {
	s.enforceLimits()
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
	now := time.Now()
	for _, item := range s.items {
		if item.Expired(now) {
			continue
		}
		if item.Metadata.Tags != nil && item.Metadata.Tags[key] == value {
			return true
		}
	}
	return false
}

// Add appends a new item and saves.
func (s *VectorStore) Add(item MemoryItem) error {
	s.Prune()
	item = s.prepareItem(item)
	if duplicate := s.duplicateEmbedding(item); duplicate != nil {
		return fmt.Errorf("%w: %s", ErrDuplicateEmbedding, duplicate.ID)
	}
	s.items = append(s.items, item)
	s.enforceLimits()
	return s.Save()
}

// Expired reports whether the item should no longer be surfaced.
func (m MemoryItem) Expired(now time.Time) bool {
	return m.ExpiresAt != nil && !m.ExpiresAt.After(now)
}

// Items returns a copy of the currently loaded items.
func (s *VectorStore) Items() []MemoryItem {
	s.Prune()
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

// StartCompaction prunes expired items on a background interval until ctx ends.
func (s *VectorStore) StartCompaction(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Prune()
			}
		}
	}()
	return done
}

// Search returns top-k items sorted by cosine similarity to the query embedding.
func (s *VectorStore) Search(query []float32, k int) []SearchResult {
	if k <= 0 || len(s.items) == 0 {
		return nil
	}

	s.Prune()
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

func (s *VectorStore) prepareItem(item MemoryItem) MemoryItem {
	if item.TS.IsZero() {
		item.TS = time.Now().UTC()
	}
	if item.ExpiresAt == nil {
		opts := s.options()
		if opts.DefaultTTL > 0 {
			expires := item.TS.Add(opts.DefaultTTL)
			item.ExpiresAt = &expires
		}
	}
	return item
}

func (s *VectorStore) duplicateEmbedding(item MemoryItem) *MemoryItem {
	if len(item.Embedding) == 0 {
		return nil
	}
	now := time.Now()
	for i := range s.items {
		existing := &s.items[i]
		if existing.Expired(now) || len(existing.Embedding) == 0 {
			continue
		}
		if len(existing.Embedding) != len(item.Embedding) {
			continue
		}
		if cosineSimilarity(existing.Embedding, item.Embedding) >= duplicateEmbeddingThreshold {
			return existing
		}
	}
	return nil
}

func (s *VectorStore) enforceLimits() {
	s.Prune()
	opts := s.options()
	if opts.MaxItemsPerScope > 0 {
		s.evictByScope(opts.MaxItemsPerScope)
	}
	if opts.MaxStoreBytes > 0 {
		s.evictBySize(opts.MaxStoreBytes)
	}
}

func (s *VectorStore) evictByScope(maxItems int) {
	byScope := make(map[string][]int)
	for i, item := range s.items {
		byScope[item.Scope()] = append(byScope[item.Scope()], i)
	}
	remove := make(map[int]struct{})
	for _, indexes := range byScope {
		if len(indexes) <= maxItems {
			continue
		}
		sort.Slice(indexes, func(i, j int) bool {
			return s.items[indexes[i]].TS.Before(s.items[indexes[j]].TS)
		})
		for _, idx := range indexes[:len(indexes)-maxItems] {
			remove[idx] = struct{}{}
		}
	}
	if len(remove) == 0 {
		return
	}
	s.removeIndexes(remove)
}

func (s *VectorStore) evictBySize(maxBytes int64) {
	for int64(s.encodedSize()) > maxBytes && len(s.items) > 0 {
		oldest := 0
		for i := 1; i < len(s.items); i++ {
			if s.items[i].TS.Before(s.items[oldest].TS) {
				oldest = i
			}
		}
		s.removeIndexes(map[int]struct{}{oldest: {}})
	}
}

func (s *VectorStore) encodedSize() int {
	data, err := json.Marshal(s.items)
	if err != nil {
		return 0
	}
	return len(data)
}

func (s *VectorStore) removeIndexes(remove map[int]struct{}) {
	kept := s.items[:0]
	for i, item := range s.items {
		if _, ok := remove[i]; ok {
			continue
		}
		kept = append(kept, item)
	}
	s.items = append([]MemoryItem(nil), kept...)
}

func (s *VectorStore) options() VectorStoreOptions {
	return normalizeOptions(s.opts)
}

func normalizeOptions(opts VectorStoreOptions) VectorStoreOptions {
	defaults := DefaultVectorStoreOptions()
	if opts.DefaultTTL == 0 {
		opts.DefaultTTL = defaults.DefaultTTL
	}
	if opts.MaxItemsPerScope == 0 {
		opts.MaxItemsPerScope = defaults.MaxItemsPerScope
	}
	if opts.MaxStoreBytes == 0 {
		opts.MaxStoreBytes = defaults.MaxStoreBytes
	}
	return opts
}

// Scope returns the item's eviction scope.
func (m MemoryItem) Scope() string {
	if m.Metadata.Tags != nil {
		if scope := m.Metadata.Tags["scope"]; scope != "" {
			return scope
		}
	}
	if m.Category != "" {
		return m.Category
	}
	return "default"
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

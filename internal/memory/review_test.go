package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAuditEntriesIncludesManualAndWorkspaceMemory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "MEMORY.md"), []byte("# Decisions\n- keep replay notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewWorkspaceVectorStore(workspace)
	if err := store.Add(MemoryItem{
		ID:      "mem-1",
		Content: "persist replay artifacts",
		Metadata: Metadata{
			Tags: map[string]string{"scope": "workspace", "origin": "manual-promote", "confidence": "high"},
		},
		TS: time.Date(2026, 3, 24, 4, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadAuditEntries(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("LoadAuditEntries() returned %d entries, want 2", len(entries))
	}

	var sawManual, sawVector bool
	for _, entry := range entries {
		switch entry.Source {
		case "MEMORY.md":
			sawManual = strings.Contains(entry.Content, "Decisions: - keep replay notes")
		case "workspace-memory":
			sawVector = entry.ID == "mem-1" && entry.Confidence == "high" && entry.Origin == "manual-promote"
		}
	}
	if !sawManual {
		t.Fatal("missing manual MEMORY.md audit entry")
	}
	if !sawVector {
		t.Fatal("missing workspace vector audit entry")
	}
}

func TestForgetAuditEntryRemovesWorkspaceVectorEntry(t *testing.T) {
	workspace := t.TempDir()
	store := NewWorkspaceVectorStore(workspace)
	if err := store.Add(MemoryItem{ID: "mem-1", Content: "persist replay artifacts"}); err != nil {
		t.Fatal(err)
	}

	if err := ForgetAuditEntry(workspace, "mem-1"); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadAuditEntries(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("LoadAuditEntries() returned %d entries after forget, want 0", len(entries))
	}
}

func TestForgetAuditEntryRemovesManualMemoryLine(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "MEMORY.md")
	if err := os.WriteFile(path, []byte("- first note\n- second note\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ForgetAuditEntry(workspace, "MEMORY.md:1"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "first note") {
		t.Fatalf("manual memory line was not removed: %q", string(data))
	}
	if !strings.Contains(string(data), "second note") {
		t.Fatalf("manual memory removal corrupted remaining content: %q", string(data))
	}
}

func TestLoadAuditEntriesPrunesExpiredWorkspaceMemory(t *testing.T) {
	workspace := t.TempDir()
	store := NewWorkspaceVectorStore(workspace)
	past := time.Now().Add(-time.Hour)
	if err := store.Add(MemoryItem{
		ID:        "mem-expired",
		Content:   "temporary note",
		Category:  "note",
		ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(MemoryItem{
		ID:      "mem-live",
		Content: "keep this fact",
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadAuditEntries(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "mem-live" {
		t.Fatalf("unexpected entries after prune: %+v", entries)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "blackboard.vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "mem-expired") {
		t.Fatalf("expired memory was not pruned from store: %s", text)
	}
}

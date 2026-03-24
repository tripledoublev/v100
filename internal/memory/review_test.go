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

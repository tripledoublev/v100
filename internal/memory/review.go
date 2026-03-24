package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AuditEntry struct {
	ID         string
	Content    string
	Source     string
	Provenance string
	Scope      string
	Origin     string
	Confidence string
	Timestamp  time.Time
	Tags       map[string]string
}

func LoadAuditEntries(workspaceDir string) ([]AuditEntry, error) {
	entries := make([]AuditEntry, 0, 16)

	memPath := filepath.Join(workspaceDir, "MEMORY.md")
	if data, err := os.ReadFile(memPath); err == nil {
		entries = append(entries, parseManualAuditEntries(string(data))...)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	store := NewWorkspaceVectorStore(workspaceDir)
	if err := store.Load(); err != nil {
		return nil, err
	}
	for _, item := range store.Items() {
		scope := item.Metadata.Tags["scope"]
		if scope == "" {
			scope = "workspace"
		}
		origin := item.Metadata.Tags["origin"]
		if origin == "" {
			origin = "tool:blackboard_store"
		}
		confidence := item.Metadata.Tags["confidence"]
		if confidence == "" {
			confidence = "stored"
		}
		entries = append(entries, AuditEntry{
			ID:         item.ID,
			Content:    strings.TrimSpace(item.Content),
			Source:     "workspace-memory",
			Provenance: item.ID,
			Scope:      scope,
			Origin:     origin,
			Confidence: confidence,
			Timestamp:  item.TS,
			Tags:       item.Metadata.Tags,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].Timestamp.After(entries[j].Timestamp)
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}

func parseManualAuditEntries(text string) []AuditEntry {
	lines := strings.Split(text, "\n")
	entries := make([]AuditEntry, 0, len(lines))
	section := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			section = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		content := line
		if section != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(section)) {
			content = section + ": " + line
		}
		entries = append(entries, AuditEntry{
			ID:         "MEMORY.md:" + strconv.Itoa(i+1),
			Content:    content,
			Source:     "MEMORY.md",
			Provenance: "MEMORY.md:" + strconv.Itoa(i+1),
			Scope:      "workspace",
			Origin:     "manual-note",
			Confidence: "manual",
		})
	}
	return entries
}

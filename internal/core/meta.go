package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/providers"
)

// RunMeta holds metadata for a run, persisted as meta.json in the run directory.
type RunMeta struct {
	RunID             string                  `json:"run_id"`
	Name              string                  `json:"name,omitempty"`
	Tags              map[string]string       `json:"tags,omitempty"`
	Provider          string                  `json:"provider"`
	Model             string                  `json:"model"`
	ModelMetadata     providers.ModelMetadata `json:"model_metadata,omitempty"`
	SourceWorkspace   string                  `json:"source_workspace,omitempty"`
	SourceFingerprint string                  `json:"source_fingerprint,omitempty"`
	Sandbox           config.SandboxConfig    `json:"sandbox,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	Score             string                  `json:"score,omitempty"` // pass|fail|partial
	ScoreNotes        string                  `json:"score_notes,omitempty"`
}

// WriteMeta writes meta.json to the given directory.
func WriteMeta(dir string, m RunMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("meta: marshal: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o644)
}

// ReadMeta reads meta.json from the given directory.
func ReadMeta(dir string) (RunMeta, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return RunMeta{}, fmt.Errorf("meta: read: %w", err)
	}
	var m RunMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return RunMeta{}, fmt.Errorf("meta: parse: %w", err)
	}
	return m, nil
}

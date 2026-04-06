package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestResearchLoadConfig(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir := t.TempDir()

	// Create valid research config
	configPath := filepath.Join(tmpDir, "research.toml")
	configContent := `
name = "test-research"
branch_prefix = "research"

[target]
file = "train.py"
context = []
program = "program.md"

[experiment]
command = "echo test"
timeout = "10s"
metric = "score"
direction = "lower"

[budget]
steps = 5
cost_usd = 0.1
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Load config using real core.LoadResearchConfig
	researchCfg, err := core.LoadResearchConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Verify config loaded correctly
	if researchCfg.Name != "test-research" {
		t.Errorf("name = %q, want test-research", researchCfg.Name)
	}
	if researchCfg.Target.File != "train.py" {
		t.Errorf("target file = %q, want train.py", researchCfg.Target.File)
	}
	if researchCfg.Experiment.Metric != "score" {
		t.Errorf("metric = %q, want score", researchCfg.Experiment.Metric)
	}
	if researchCfg.Experiment.Direction != "lower" {
		t.Errorf("direction = %q, want lower", researchCfg.Experiment.Direction)
	}
	if researchCfg.Budget.Steps != 5 {
		t.Errorf("budget steps = %d, want 5", researchCfg.Budget.Steps)
	}
}

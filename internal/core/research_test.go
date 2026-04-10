package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResearchConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		valid   bool
		errMsg  string
	}{
		{
			name: "valid config",
			content: `
name = "test"
branch_prefix = "research"

[target]
file = "train.py"
context = ["README.md"]
program = "program.md"

[experiment]
command = "echo test"
timeout = "5m"
metric = "val_bpb"
direction = "lower"

[budget]
steps = 20
cost_usd = 1.0
`,
			valid: true,
		},
		{
			name: "missing name",
			content: `
[target]
file = "train.py"
program = "program.md"

[experiment]
command = "echo test"
metric = "val_bpb"
direction = "lower"
`,
			valid:  false,
			errMsg: "missing required field: name",
		},
		{
			name: "invalid direction",
			content: `
name = "test"

[target]
file = "train.py"
program = "program.md"

[experiment]
command = "echo test"
metric = "val_bpb"
direction = "invalid"
`,
			valid:  false,
			errMsg: "direction must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "test.toml")
			if err := os.WriteFile(configPath, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cfg, err := LoadResearchConfig(configPath)

			if tt.valid {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if cfg == nil {
					t.Error("config is nil")
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestResearchConfigDefaults(t *testing.T) {
	content := `
name = "test"

[target]
file = "train.py"
program = "program.md"

[experiment]
command = "echo test"
metric = "val_bpb"
direction = "lower"
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadResearchConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Budget.Steps != 20 {
		t.Errorf("default steps = %d, want 20", cfg.Budget.Steps)
	}
	if cfg.Budget.CostUSD != 1.0 {
		t.Errorf("default cost = %f, want 1.0", cfg.Budget.CostUSD)
	}
	if cfg.BranchPrefix != "research" {
		t.Errorf("default branch_prefix = %q, want research", cfg.BranchPrefix)
	}
	if cfg.Experiment.WorkDir != "." {
		t.Errorf("default workdir = %q, want .", cfg.Experiment.WorkDir)
	}
	if cfg.Experiment.LogFile != "run.log" {
		t.Errorf("default log_file = %q, want run.log", cfg.Experiment.LogFile)
	}
}

func TestResearchConfigWandBDefaults(t *testing.T) {
	content := `
name = "test"

[target]
file = "train.py"
program = "program.md"

[experiment]
command = "echo test"
metric = "val_bpb"
direction = "lower"

[experiment.tracking.wandb]
enabled = true
project = "autoresearch"
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadResearchConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Experiment.Tracking.WandB.APIKeyEnv != "WANDB_API_KEY" {
		t.Fatalf("api_key_env = %q, want WANDB_API_KEY", cfg.Experiment.Tracking.WandB.APIKeyEnv)
	}
}

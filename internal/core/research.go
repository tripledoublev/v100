package core

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// ResearchConfig defines the research experiment configuration.
type ResearchConfig struct {
	Name         string               `toml:"name"`
	BranchPrefix string               `toml:"branch_prefix"`
	Target       ResearchTarget       `toml:"target"`
	Experiment   ResearchExperiment   `toml:"experiment"`
	Budget       ResearchBudget       `toml:"budget"`
	Compute      ResearchComputeConfig `toml:"compute"`
}

// ResearchComputeConfig describes the GPU compute provider for experiment execution.
type ResearchComputeConfig struct {
	Provider    string `toml:"provider"`     // "local" (default), "modal"
	GPU         string `toml:"gpu"`          // e.g. "A100", "T4"
	Image       string `toml:"image"`        // container image (Modal)
	Timeout     string `toml:"timeout"`      // duration string e.g. "30m"
	ModalSecret string `toml:"modal_secret"` // named Modal secret for env injection
}

// ResearchTarget describes the file and context for agent modifications.
type ResearchTarget struct {
	File    string   `toml:"file"`
	Context []string `toml:"context"`
	Program string   `toml:"program"`
}

// ResearchExperiment describes how to run and evaluate experiments.
type ResearchExperiment struct {
	Command     string                 `toml:"command"`
	Timeout     string                 `toml:"timeout"`
	Metric      string                 `toml:"metric"`
	Direction   string                 `toml:"direction"` // "lower" or "higher"
	WorkDir     string                 `toml:"workdir"`
	LogFile     string                 `toml:"log_file"`
	Setup       string                 `toml:"setup"`
	Collect     string                 `toml:"collect"`
	Env         map[string]string      `toml:"env"`
	MetricRegex string                 `toml:"metric_regex"`
	MemoryRegex string                 `toml:"memory_regex"`
	Tracking    ResearchTrackingConfig `toml:"tracking"`
}

// ResearchTrackingConfig defines optional experiment tracking integrations.
type ResearchTrackingConfig struct {
	WandB ResearchWandBConfig `toml:"wandb"`
}

// ResearchWandBConfig defines Weights & Biases environment wiring.
type ResearchWandBConfig struct {
	Enabled        bool     `toml:"enabled"`
	Project        string   `toml:"project"`
	Entity         string   `toml:"entity"`
	Mode           string   `toml:"mode"`
	Group          string   `toml:"group"`
	JobType        string   `toml:"job_type"`
	Tags           []string `toml:"tags"`
	RunName        string   `toml:"run_name"`
	Notes          string   `toml:"notes"`
	APIKeyEnv      string   `toml:"api_key_env"`
	BaseURL        string   `toml:"base_url"`
	GitAutoDisable bool     `toml:"git_auto_disable"`
}

// ResearchBudget specifies resource constraints per round.
type ResearchBudget struct {
	Steps   int     `toml:"steps"`
	CostUSD float64 `toml:"cost_usd"`
}

// LoadResearchConfig parses a TOML research config file.
func LoadResearchConfig(path string) (*ResearchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg ResearchConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Validation
	if cfg.Name == "" {
		return nil, fmt.Errorf("config missing required field: name")
	}
	if cfg.Target.File == "" {
		return nil, fmt.Errorf("config missing required field: target.file")
	}
	if cfg.Target.Program == "" {
		return nil, fmt.Errorf("config missing required field: target.program")
	}
	if cfg.Experiment.Command == "" {
		return nil, fmt.Errorf("config missing required field: experiment.command")
	}
	if cfg.Experiment.Metric == "" {
		return nil, fmt.Errorf("config missing required field: experiment.metric")
	}
	if cfg.Experiment.Direction != "lower" && cfg.Experiment.Direction != "higher" {
		return nil, fmt.Errorf("experiment.direction must be 'lower' or 'higher'")
	}
	if cfg.Budget.Steps == 0 {
		cfg.Budget.Steps = 20
	}
	if cfg.Budget.CostUSD == 0 {
		cfg.Budget.CostUSD = 1.0
	}
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = "research"
	}
	if cfg.Experiment.WorkDir == "" {
		cfg.Experiment.WorkDir = "."
	}
	if cfg.Experiment.LogFile == "" {
		cfg.Experiment.LogFile = "run.log"
	}
	if cfg.Experiment.Tracking.WandB.Enabled && cfg.Experiment.Tracking.WandB.APIKeyEnv == "" {
		cfg.Experiment.Tracking.WandB.APIKeyEnv = "WANDB_API_KEY"
	}

	return &cfg, nil
}

// ResearchRound represents a single autonomous experiment round.
type ResearchRound struct {
	RoundNum    int
	CommitHash  string
	Metric      float64
	Status      string // "keep", "discard", "crash"
	Description string
	MemoryGB    float64
	Timestamp   time.Time
}

// ResearchResult represents the complete TSV row for a round.
type ResearchResult struct {
	Commit      string  // short hash
	Metric      float64 // parsed from output
	MemoryGB    float64 // if captured from output
	Status      string  // keep, discard, crash
	Description string
}

package core

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// ResearchConfig defines the research experiment configuration.
type ResearchConfig struct {
	Name         string             `toml:"name"`
	BranchPrefix string             `toml:"branch_prefix"`
	Target       ResearchTarget     `toml:"target"`
	Experiment   ResearchExperiment `toml:"experiment"`
	Budget       ResearchBudget     `toml:"budget"`
}

// ResearchTarget describes the file and context for agent modifications.
type ResearchTarget struct {
	File    string   `toml:"file"`
	Context []string `toml:"context"`
	Program string   `toml:"program"`
}

// ResearchExperiment describes how to run and evaluate experiments.
type ResearchExperiment struct {
	Command   string `toml:"command"`
	Timeout   string `toml:"timeout"`
	Metric    string `toml:"metric"`
	Direction string `toml:"direction"` // "lower" or "higher"
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

	return &cfg, nil
}

// ResearchRound represents a single autonomous experiment round.
type ResearchRound struct {
	RoundNum      int
	CommitHash    string
	Metric        float64
	Status        string // "keep", "discard", "crash"
	Description   string
	MemoryGB      float64
	Timestamp     time.Time
}

// ResearchResult represents the complete TSV row for a round.
type ResearchResult struct {
	Commit      string  // short hash
	Metric      float64 // parsed from output
	MemoryGB    float64 // if captured from output
	Status      string  // keep, discard, crash
	Description string
}

package core

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// BenchConfig is the top-level bench file format.
type BenchConfig struct {
	Name     string         `toml:"name"`
	Prompts  []BenchPrompt  `toml:"prompts"`
	Variants []BenchVariant `toml:"variants"`
}

// BenchPrompt is a single prompt in a bench config.
type BenchPrompt struct {
	Message string `toml:"message"`
}

// BenchVariant describes a provider/model configuration to test.
type BenchVariant struct {
	Name        string `toml:"name"`
	Provider    string `toml:"provider"`
	Model       string `toml:"model"`
	BudgetSteps int    `toml:"budget_steps"`
}

// LoadBenchConfig parses a TOML bench file.
func LoadBenchConfig(path string) (*BenchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bench: read %s: %w", path, err)
	}
	var bc BenchConfig
	if _, err := toml.Decode(string(data), &bc); err != nil {
		return nil, fmt.Errorf("bench: parse %s: %w", path, err)
	}
	if len(bc.Prompts) == 0 {
		return nil, fmt.Errorf("bench: no prompts defined")
	}
	if len(bc.Variants) == 0 {
		return nil, fmt.Errorf("bench: no variants defined")
	}
	return &bc, nil
}

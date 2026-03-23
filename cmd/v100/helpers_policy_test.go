package main

import (
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestLoadPolicyAppliesDefaultMaxToolCallsPerStep(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.MaxToolCallsPerStep = 7
	cfg.Policies["default"] = config.PolicyConfig{}

	p := loadPolicy(cfg, "default")
	if p.MaxToolCallsPerStep != 7 {
		t.Fatalf("MaxToolCallsPerStep = %d, want 7", p.MaxToolCallsPerStep)
	}
}

func TestLoadPolicyKeepsNamedPolicyMaxToolCallsWhenDefaultUnset(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.MaxToolCallsPerStep = 0
	cfg.Policies["default"] = config.PolicyConfig{MaxToolCallsPerStep: 13}

	p := loadPolicy(cfg, "default")
	if p.MaxToolCallsPerStep != 13 {
		t.Fatalf("MaxToolCallsPerStep = %d, want 13", p.MaxToolCallsPerStep)
	}
}

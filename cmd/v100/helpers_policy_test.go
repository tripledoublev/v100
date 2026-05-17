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

func TestLoadPolicyAppliesStaleToolElideSteps(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.StaleToolElideSteps = 42
	cfg.Policies["default"] = config.PolicyConfig{}

	p := loadPolicy(cfg, "default")
	if p.StaleToolElideSteps != 42 {
		t.Fatalf("StaleToolElideSteps = %d, want 42", p.StaleToolElideSteps)
	}
}

func TestLoadPolicyPreservesStaleToolElideDisabled(t *testing.T) {
	// -1 means "explicitly disabled"; must survive the != 0 guard.
	cfg := config.DefaultConfig()
	cfg.Defaults.StaleToolElideSteps = -1
	cfg.Policies["default"] = config.PolicyConfig{}

	p := loadPolicy(cfg, "default")
	if p.StaleToolElideSteps != -1 {
		t.Fatalf("StaleToolElideSteps = %d, want -1 (disabled)", p.StaleToolElideSteps)
	}
}

func TestBuildSolverAcceptsDualChannel(t *testing.T) {
	cfg := config.DefaultConfig()
	solver, err := buildSolver(cfg, "dual_channel")
	if err != nil {
		t.Fatal(err)
	}
	if solver.Name() != "dual_channel" {
		t.Fatalf("unexpected solver %T", solver)
	}
}

func TestBuildToolRegistryRegistersATProtoUploadBlob(t *testing.T) {
	cfg := config.DefaultConfig()
	reg := buildToolRegistry(cfg)
	if _, ok := reg.Lookup("atproto_upload_blob"); !ok {
		t.Fatal("atproto_upload_blob should be registered")
	}
}

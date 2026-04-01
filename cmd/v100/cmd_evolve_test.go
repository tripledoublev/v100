package main

import (
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
)

func TestAvgScore(t *testing.T) {
	results := []evolveRunResult{
		{Value: 1.0},
		{Value: 0.5},
		{Value: 0.0},
	}
	avg := avgScore(results)
	if avg != 0.5 {
		t.Errorf("expected 0.5, got %f", avg)
	}
}

func TestAvgScoreEmpty(t *testing.T) {
	avg := avgScore(nil)
	if avg != 0 {
		t.Errorf("expected 0, got %f", avg)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Errorf("expected 'hello...', got %q", truncate("hello world", 5))
	}
}

func TestResolveModelFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	model := resolveModel(cfg, "nonexistent")
	if model != "" {
		t.Errorf("expected empty model for unknown provider, got %q", model)
	}
}

func TestEvolveCommandRegistered(t *testing.T) {
	root := rootCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "evolve" {
			// Check subcommands
			var found []string
			for _, sub := range cmd.Commands() {
				found = append(found, sub.Name())
			}
			if len(found) < 2 {
				t.Errorf("expected at least 2 subcommands (once, adopt), got %v", found)
			}
			return
		}
	}
	t.Error("evolve command not registered")
}

func TestResolveBenchProviderModelUsesFallbacks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers["gemini"] = config.ProviderConfig{Type: "gemini", DefaultModel: "gemini-default"}

	providerName, model := resolveBenchProviderModel(cfg, core.BenchVariant{}, "gemini")
	if providerName != "gemini" {
		t.Fatalf("providerName = %q, want gemini", providerName)
	}
	if model != "gemini-default" {
		t.Fatalf("model = %q, want gemini-default", model)
	}

	providerName, model = resolveBenchProviderModel(cfg, core.BenchVariant{Provider: "codex", Model: "gpt-5.4"}, "gemini")
	if providerName != "codex" || model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.4", providerName, model)
	}
}

func TestNewEvolveBenchMetaSetsParentRunID(t *testing.T) {
	meta := newEvolveBenchMeta("child-run", "parent-run", "ux", "candidate", "fast", "codex", "gpt-5.4", 1)
	if meta.RunID != "child-run" {
		t.Fatalf("RunID = %q, want child-run", meta.RunID)
	}
	if meta.ParentRunID != "parent-run" {
		t.Fatalf("ParentRunID = %q, want parent-run", meta.ParentRunID)
	}
	if meta.Provider != "codex" || meta.Model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.4", meta.Provider, meta.Model)
	}
	if got := meta.Tags["policy_variant"]; got != "candidate" {
		t.Fatalf("policy_variant = %q, want candidate", got)
	}
	if got := meta.Tags["variant"]; got != "fast" {
		t.Fatalf("variant = %q, want fast", got)
	}
	if got := meta.Tags["prompt_id"]; got != "2" {
		t.Fatalf("prompt_id = %q, want 2", got)
	}
}

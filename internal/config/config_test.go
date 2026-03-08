package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Provider != "codex" {
		t.Errorf("expected default provider codex, got %s", cfg.Defaults.Provider)
	}
	if cfg.Defaults.BudgetSteps != 50 {
		t.Errorf("expected 50 budget steps, got %d", cfg.Defaults.BudgetSteps)
	}

	// Check that anthropic provider exists in defaults
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Error("expected anthropic provider in defaults")
	}
	if cfg.Providers["anthropic"].Type != "anthropic" {
		t.Errorf("expected type anthropic, got %s", cfg.Providers["anthropic"].Type)
	}
	if cfg.Providers["anthropic"].Auth.Env != "ANTHROPIC_API_KEY" {
		t.Errorf("expected ANTHROPIC_API_KEY, got %s", cfg.Providers["anthropic"].Auth.Env)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	toml := `
[providers.openai]
type = "openai"
default_model = "gpt-4o"
base_url = "https://api.openai.com/v1"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[providers.anthropic]
type = "anthropic"
default_model = "claude-sonnet-4-20250514"
[providers.anthropic.auth]
env = "ANTHROPIC_API_KEY"

[tools]
enabled = ["fs_read"]
dangerous = []

[defaults]
provider = "anthropic"
budget_steps = 25
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Defaults.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %s", cfg.Defaults.Provider)
	}
	if cfg.Defaults.BudgetSteps != 25 {
		t.Errorf("expected 25 budget steps, got %d", cfg.Defaults.BudgetSteps)
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Error("expected anthropic in providers")
	}
}

func TestLoadConfigGenParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	toml := `
[providers.openai]
type = "openai"
default_model = "gpt-4o"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[tools]
enabled = ["fs_read"]
dangerous = []

[defaults]
provider = "openai"
temperature = 0.7
top_p = 0.9
max_tokens = 2048
seed = 42
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Defaults.Temperature == nil || *cfg.Defaults.Temperature != 0.7 {
		t.Error("expected temperature 0.7")
	}
	if cfg.Defaults.TopP == nil || *cfg.Defaults.TopP != 0.9 {
		t.Error("expected top_p 0.9")
	}
	if cfg.Defaults.MaxTokens != 2048 {
		t.Errorf("expected max_tokens 2048, got %d", cfg.Defaults.MaxTokens)
	}
	if cfg.Defaults.Seed == nil || *cfg.Defaults.Seed != 42 {
		t.Error("expected seed 42")
	}
}

func TestDefaultTOMLContainsAnthropic(t *testing.T) {
	toml := DefaultTOML()
	if !contains(toml, "[providers.anthropic]") {
		t.Error("default TOML should contain anthropic provider section")
	}
	if !contains(toml, "ANTHROPIC_API_KEY") {
		t.Error("default TOML should reference ANTHROPIC_API_KEY")
	}
	if !contains(toml, "[sandbox]") {
		t.Error("default TOML should contain sandbox section")
	}
	if !contains(toml, `network_tier = "off"`) {
		t.Error("default TOML should default sandbox network_tier to off")
	}
	if !contains(toml, `image = "google/gemini-v100-research:latest"`) {
		t.Error("default TOML should contain sandbox image")
	}
	if !contains(toml, `memory_mb = 512`) {
		t.Error("default TOML should contain sandbox memory limit")
	}
	if !contains(toml, `cpus = 1.0`) {
		t.Error("default TOML should contain sandbox cpu limit")
	}
}

func TestLoadConfigAppliesSandboxDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	toml := `
[providers.openai]
type = "openai"
default_model = "gpt-4o"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[tools]
enabled = ["fs_read"]
dangerous = []

[defaults]
provider = "openai"

[sandbox]
backend = "docker"
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sandbox.Backend != "docker" {
		t.Fatalf("sandbox backend = %q, want docker", cfg.Sandbox.Backend)
	}
	if cfg.Sandbox.Image == "" || cfg.Sandbox.NetworkTier == "" || cfg.Sandbox.MemoryMB <= 0 || cfg.Sandbox.CPUs <= 0 || cfg.Sandbox.ApplyBack == "" {
		t.Fatalf("sandbox defaults not applied: %+v", cfg.Sandbox)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

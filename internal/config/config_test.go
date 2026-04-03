package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Provider != "minimax" {
		t.Errorf("expected default provider minimax, got %s", cfg.Defaults.Provider)
	}
	if cfg.Defaults.BudgetSteps != 50 {
		t.Errorf("expected 50 budget steps, got %d", cfg.Defaults.BudgetSteps)
	}
	if cfg.Defaults.MemoryMode != "auto" {
		t.Errorf("expected default memory mode auto, got %q", cfg.Defaults.MemoryMode)
	}
	if cfg.Defaults.MemoryMaxTokens != 256 {
		t.Errorf("expected default memory max tokens 256, got %d", cfg.Defaults.MemoryMaxTokens)
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

	// Check that minimax provider exists in defaults
	if _, ok := cfg.Providers["minimax"]; !ok {
		t.Error("expected minimax provider in defaults")
	}
	if cfg.Providers["minimax"].Type != "minimax" {
		t.Errorf("expected type minimax, got %s", cfg.Providers["minimax"].Type)
	}
	if cfg.Providers["minimax"].DefaultModel != "MiniMax-M2.7" {
		t.Errorf("expected MiniMax-M2.7, got %s", cfg.Providers["minimax"].DefaultModel)
	}

	// Verify sh tool is enabled and dangerous by default
	shEnabled := false
	for _, tool := range cfg.Tools.Enabled {
		if tool == "sh" {
			shEnabled = true
			break
		}
	}
	if !shEnabled {
		t.Error("expected sh tool to be enabled by default")
	}
	if !containsString(cfg.Tools.Enabled, "news_fetch") {
		t.Error("expected news_fetch tool to be enabled by default")
	}

	shDangerous := false
	for _, tool := range cfg.Tools.Dangerous {
		if tool == "sh" {
			shDangerous = true
			break
		}
	}
	if !shDangerous {
		t.Error("expected sh tool to be dangerous by default")
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

	// Verify sh is migrated if missing
	shEnabled := false
	for _, tool := range cfg.Tools.Enabled {
		if tool == "sh" {
			shEnabled = true
			break
		}
	}
	if !shEnabled {
		t.Error("expected sh tool to be enabled after migration")
	}
	if !containsString(cfg.Tools.Enabled, "news_fetch") {
		t.Error("expected news_fetch tool to be enabled after migration")
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
	if !contains(toml, "[providers.minimax]") {
		t.Error("default TOML should contain minimax provider section")
	}
	if !contains(toml, "MiniMax-M2.7") {
		t.Error("default TOML should reference MiniMax-M2.7 model")
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
	if !contains(toml, `memory_mode = "auto"`) {
		t.Error("default TOML should contain memory_mode")
	}
	if !contains(toml, `memory_max_tokens = 256`) {
		t.Error("default TOML should contain memory_max_tokens")
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

func TestLoadConfigAppliesDefaultToolsWhenToolsSectionMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	toml := `
[defaults]
provider = "minimax"

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

	defaults := DefaultConfig().Tools
	for _, want := range defaults.Enabled {
		if !containsString(cfg.Tools.Enabled, want) {
			t.Fatalf("expected enabled tools to include %q, got %v", want, cfg.Tools.Enabled)
		}
	}
	for _, want := range defaults.Dangerous {
		if !containsString(cfg.Tools.Dangerous, want) {
			t.Fatalf("expected dangerous tools to include %q, got %v", want, cfg.Tools.Dangerous)
		}
	}
}

func TestLoadConfigPreservesExplicitToolAllowlist(t *testing.T) {
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
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if !containsString(cfg.Tools.Enabled, "fs_read") {
		t.Fatalf("expected explicit enabled tools to include fs_read, got %v", cfg.Tools.Enabled)
	}
	if containsString(cfg.Tools.Enabled, "fs_write") {
		t.Fatalf("expected explicit enabled tools to remain custom, got %v", cfg.Tools.Enabled)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

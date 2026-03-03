package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the root configuration structure.
type Config struct {
	Providers map[string]ProviderConfig `toml:"providers"`
	Tools     ToolsConfig               `toml:"tools"`
	Policies  map[string]PolicyConfig   `toml:"policies"`
	Defaults  DefaultsConfig            `toml:"defaults"`
}

// ProviderConfig holds per-provider settings.
type ProviderConfig struct {
	Type         string     `toml:"type"`
	DefaultModel string     `toml:"default_model"`
	BaseURL      string     `toml:"base_url"`
	Auth         AuthConfig `toml:"auth"`
}

// AuthConfig describes how to obtain credentials.
type AuthConfig struct {
	Env string `toml:"env"` // environment variable name
}

// ToolsConfig lists enabled and dangerous tools.
type ToolsConfig struct {
	Enabled   []string `toml:"enabled"`
	Dangerous []string `toml:"dangerous"`
}

// PolicyConfig points to a policy definition.
type PolicyConfig struct {
	SystemPromptPath    string `toml:"system_prompt_path"`
	MaxToolCallsPerStep int    `toml:"max_tool_calls_per_step"`
}

// DefaultsConfig holds run-level defaults.
type DefaultsConfig struct {
	Provider            string  `toml:"provider"`
	ConfirmTools        string  `toml:"confirm_tools"` // always | dangerous | never
	BudgetSteps         int     `toml:"budget_steps"`
	BudgetTokens        int     `toml:"budget_tokens"`
	BudgetCostUSD       float64 `toml:"budget_cost_usd"`
	ToolTimeoutMS       int     `toml:"tool_timeout_ms"`
	MaxToolCallsPerStep int     `toml:"max_tool_calls_per_step"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() *Config {
	return &Config{
		Providers: map[string]ProviderConfig{
			"codex": {
				Type:         "codex",
				DefaultModel: "gpt-5.3-codex",
			},
			"openai": {
				Type:         "openai",
				DefaultModel: "gpt-4o",
				BaseURL:      "https://api.openai.com/v1",
				Auth:         AuthConfig{Env: "OPENAI_API_KEY"},
			},
		},
		Tools: ToolsConfig{
			Enabled: []string{
				"fs.read", "fs.write", "fs.list", "fs.mkdir",
				"git.status", "git.diff", "project.search", "patch.apply",
			},
			Dangerous: []string{"fs.write", "sh", "git.commit", "patch.apply"},
		},
		Policies: map[string]PolicyConfig{
			"default": {
				SystemPromptPath:    "~/.config/v100/policies/default.md",
				MaxToolCallsPerStep: 5,
			},
		},
		Defaults: DefaultsConfig{
			Provider:            "codex",
			ConfirmTools:        "dangerous",
			BudgetSteps:         50,
			BudgetTokens:        100000,
			ToolTimeoutMS:       30000,
			MaxToolCallsPerStep: 5,
		},
	}
}

// DefaultTOML returns the default config as a TOML string.
func DefaultTOML() string {
	return `# v100 agent harness configuration

# ── Codex provider (ChatGPT Plus/Pro subscription — no API billing) ──────────
# Run 'agent login' to authenticate via browser OAuth.
# Token is stored at ~/.config/v100/auth.json automatically.
[providers.codex]
type = "codex"
default_model = "gpt-5.3-codex"

# ── OpenAI API provider (pay-as-you-go) ──────────────────────────────────────
[providers.openai]
type = "openai"
default_model = "gpt-4o"
base_url = "https://api.openai.com/v1"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[tools]
enabled = ["fs.read", "fs.write", "fs.list", "fs.mkdir", "git.status", "git.diff", "project.search", "patch.apply"]
dangerous = ["fs.write", "sh", "git.commit", "patch.apply"]

[policies.default]
system_prompt_path = "~/.config/v100/policies/default.md"
max_tool_calls_per_step = 5

[defaults]
provider = "codex"            # use ChatGPT subscription by default
confirm_tools = "dangerous"   # always | dangerous | never
budget_steps = 50
budget_tokens = 100000
budget_cost_usd = 0.0
tool_timeout_ms = 30000
max_tool_calls_per_step = 5
`
}

// Load reads and parses a TOML config file, expanding ~ in paths.
func Load(path string) (*Config, error) {
	path = expandHome(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &cfg, nil
}

// XDGConfigPath returns the default XDG config path for v100.
func XDGConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "v100", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "v100", "config.toml")
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

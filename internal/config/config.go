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
	ContextLimit        int     `toml:"context_limit"` // estimated token threshold for compression (0 = disabled, default 80000)
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
				"fs_read", "fs_write", "fs_list", "fs_mkdir",
				"git_status", "git_diff", "git_push", "curl_fetch", "project_search", "patch_apply",
			},
			Dangerous: []string{"fs_write", "sh", "git_commit", "git_push", "patch_apply"},
		},
		Policies: map[string]PolicyConfig{
			"default": {
				SystemPromptPath:    "~/.config/v100/policies/default.md",
				MaxToolCallsPerStep: 20,
			},
		},
		Defaults: DefaultsConfig{
			Provider:            "codex",
			ConfirmTools:        "dangerous",
			BudgetSteps:         50,
			BudgetTokens:        100000,
			ToolTimeoutMS:       30000,
			MaxToolCallsPerStep: 20,
			ContextLimit:        80000,
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
enabled = ["fs_read", "fs_write", "fs_list", "fs_mkdir", "git_status", "git_diff", "git_push", "curl_fetch", "project_search", "patch_apply"]
dangerous = ["fs_write", "sh", "git_commit", "git_push", "patch_apply"]

[policies.default]
system_prompt_path = "~/.config/v100/policies/default.md"
max_tool_calls_per_step = 20

[defaults]
provider = "codex"            # use ChatGPT subscription by default
confirm_tools = "dangerous"   # always | dangerous | never
budget_steps = 50
budget_tokens = 100000
budget_cost_usd = 0.0
tool_timeout_ms = 30000
max_tool_calls_per_step = 20
context_limit = 80000        # estimated token threshold; compress history when exceeded (0 = disabled)
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
	// Backward-compatible tool migrations for older config files.
	ensureString(&cfg.Tools.Enabled, "git_push")
	ensureString(&cfg.Tools.Dangerous, "git_push")
	ensureString(&cfg.Tools.Enabled, "curl_fetch")
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

func ensureString(items *[]string, want string) {
	for _, s := range *items {
		if s == want {
			return
		}
	}
	*items = append(*items, want)
}

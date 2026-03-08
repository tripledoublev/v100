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
	Agents    map[string]AgentConfig    `toml:"agents"`
	Defaults  DefaultsConfig            `toml:"defaults"`
	Sandbox   SandboxConfig             `toml:"sandbox"`
}

// SandboxConfig defines the isolated execution environment.
type SandboxConfig struct {
	Enabled     bool    `toml:"enabled"`
	Backend     string  `toml:"backend"`      // host | docker
	Image       string  `toml:"image"`        // for docker backend
	NetworkTier string  `toml:"network_tier"` // off | research | open
	MemoryMB    int     `toml:"memory_mb"`
	CPUs        float64 `toml:"cpus"`
	ApplyBack   string  `toml:"apply_back"` // manual | on_success | never
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
	Streaming           bool   `toml:"streaming"`
}

// AgentConfig defines a named specialist agent role.
type AgentConfig struct {
	SystemPrompt  string   `toml:"system_prompt"`
	Tools         []string `toml:"tools"`
	Model         string   `toml:"model"`
	BudgetSteps   int      `toml:"budget_steps"`
	BudgetTokens  int      `toml:"budget_tokens"`
	BudgetCostUSD float64  `toml:"budget_cost_usd"`
}

// DefaultsConfig holds run-level defaults.
type DefaultsConfig struct {
	Provider            string   `toml:"provider"`
	SmartProvider       string   `toml:"smart_provider"` // for router solver
	CheapProvider       string   `toml:"cheap_provider"` // for router solver
	Solver              string   `toml:"solver"`         // react | plan_execute | router
	MaxReplans          int      `toml:"max_replans"`
	ConfirmTools        string   `toml:"confirm_tools"` // always | dangerous | never
	BudgetSteps         int      `toml:"budget_steps"`
	BudgetTokens        int      `toml:"budget_tokens"`
	BudgetCostUSD       float64  `toml:"budget_cost_usd"`
	ToolTimeoutMS       int      `toml:"tool_timeout_ms"`
	MaxToolCallsPerStep int      `toml:"max_tool_calls_per_step"`
	ContextLimit        int      `toml:"context_limit"` // estimated token threshold for compression (0 = disabled, default 80000)
	Temperature         *float64 `toml:"temperature"`
	TopP                *float64 `toml:"top_p"`
	TopK                *int     `toml:"top_k"`
	MaxTokens           int      `toml:"max_tokens"`
	Seed                *int     `toml:"seed"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() *Config {
	return &Config{
		Providers: map[string]ProviderConfig{
			"codex": {
				Type:         "codex",
				DefaultModel: "gpt-5.4",
			},
			"openai": {
				Type:         "openai",
				DefaultModel: "gpt-4o",
				BaseURL:      "https://api.openai.com/v1",
				Auth:         AuthConfig{Env: "OPENAI_API_KEY"},
			},
			"ollama": {
				Type:         "ollama",
				DefaultModel: "qwen3.5:2b",
				BaseURL:      "http://localhost:11434",
			},
			"gemini": {
				Type:         "gemini",
				DefaultModel: "gemini-2.5-flash",
			},
			"anthropic": {
				Type:         "anthropic",
				DefaultModel: "claude-sonnet-4-20250514",
				Auth:         AuthConfig{Env: "ANTHROPIC_API_KEY"},
			},
		},
		Tools: ToolsConfig{
			Enabled: []string{
				"fs_read", "fs_write", "fs_list", "fs_mkdir", "sh",
				"git_status", "git_diff", "git_push", "curl_fetch", "project_search", "patch_apply", "agent", "dispatch", "orchestrate", "blackboard_read", "blackboard_write",
				"sem_diff", "sem_impact", "sem_blame", "inspect_tool",
			},
			Dangerous: []string{"fs_write", "sh", "git_commit", "git_push", "patch_apply", "agent", "dispatch", "orchestrate", "blackboard_write"},
		},
		Policies: map[string]PolicyConfig{
			"default": {
				SystemPromptPath:    "~/.config/v100/policies/default.md",
				MaxToolCallsPerStep: 50,
			},
		},
		Agents: map[string]AgentConfig{
			"researcher": {
				SystemPrompt: "You are a researcher agent. Find and read relevant code and return concise findings. Do not modify files.",
				Tools:        []string{"fs_read", "fs_list", "project_search"},
				Model:        "",
				BudgetSteps:  15,
				BudgetTokens: 20000,
			},
			"implementer": {
				SystemPrompt: "You are an implementation agent. Read files first, then make focused code changes.",
				Tools:        []string{"fs_read", "fs_write", "patch_apply", "sh"},
				Model:        "",
				BudgetSteps:  30,
				BudgetTokens: 50000,
			},
			"reviewer": {
				SystemPrompt: "You are a review agent. Review diffs for bugs, regressions, and risks.",
				Tools:        []string{"fs_read", "git_diff", "project_search"},
				Model:        "",
				BudgetSteps:  10,
				BudgetTokens: 15000,
			},
		},
		Defaults: DefaultsConfig{
			Provider:            "codex",
			SmartProvider:       "gemini",
			CheapProvider:       "ollama",
			ConfirmTools:        "dangerous",
			BudgetSteps:         50,
			BudgetTokens:        100000,
			ToolTimeoutMS:       30000,
			MaxToolCallsPerStep: 50,
			ContextLimit:        80000,
		},
		Sandbox: SandboxConfig{
			Enabled:     false,
			Backend:     "host",
			Image:       "google/gemini-v100-research:latest",
			NetworkTier: "off",
			MemoryMB:    512,
			CPUs:        1.0,
			ApplyBack:   "manual",
		},
	}
}

// DefaultTOML returns the default config as a TOML string.
func DefaultTOML() string {
	return `# v100 agent harness configuration

# ── Codex provider (ChatGPT Plus/Pro subscription — no API billing) ──────────
# Put OAuth client values in ~/.config/v100/oauth_credentials.json first,
# then run 'v100 login' to authenticate via browser OAuth.
# Token is stored at ~/.config/v100/auth.json automatically.
[providers.codex]
type = "codex"
default_model = "gpt-5.4"

# ── OpenAI API provider (pay-as-you-go) ──────────────────────────────────────
[providers.openai]
type = "openai"
default_model = "gpt-4o"
base_url = "https://api.openai.com/v1"
[providers.openai.auth]
env = "OPENAI_API_KEY"

# ── Ollama local provider (no API key required) ────────────────────────────
[providers.ollama]
type = "ollama"
default_model = "qwen3.5:2b"
base_url = "http://localhost:11434"

# ── Gemini provider (Gemini Pro / Google One AI Premium — no API billing) ──
# Put OAuth client values in ~/.config/v100/oauth_credentials.json first,
# then run 'v100 login --provider gemini' to authenticate via browser OAuth.
# Token is stored at ~/.config/v100/gemini_auth.json automatically.
[providers.gemini]
type = "gemini"
default_model = "gemini-2.5-flash"

# ── Anthropic provider (pay-as-you-go API) ─────────────────────────────────
[providers.anthropic]
type = "anthropic"
default_model = "claude-sonnet-4-20250514"
[providers.anthropic.auth]
env = "ANTHROPIC_API_KEY"

[tools]
enabled = ["fs_read", "fs_write", "fs_list", "fs_mkdir", "git_status", "git_diff", "git_push", "curl_fetch", "project_search", "patch_apply", "agent", "dispatch", "orchestrate", "blackboard_read", "blackboard_write", "sem_diff", "sem_impact", "sem_blame"]
dangerous = ["fs_write", "sh", "git_commit", "git_push", "patch_apply", "agent", "dispatch", "orchestrate", "blackboard_write"]

[agents.researcher]
system_prompt = "You are a researcher agent. Find and read relevant code and return concise findings. Do not modify files."
tools = ["fs_read", "fs_list", "project_search"]
model = ""
budget_steps = 15
budget_tokens = 20000
budget_cost_usd = 0.0

[agents.implementer]
system_prompt = "You are an implementation agent. Read files first, then make focused code changes."
tools = ["fs_read", "fs_write", "patch_apply", "sh"]
model = ""
budget_steps = 30
budget_tokens = 50000
budget_cost_usd = 0.0

[agents.reviewer]
system_prompt = "You are a review agent. Review diffs for bugs, regressions, and risks."
tools = ["fs_read", "git_diff", "project_search"]
model = ""
budget_steps = 10
budget_tokens = 15000
budget_cost_usd = 0.0

[policies.default]
system_prompt_path = "~/.config/v100/policies/default.md"
max_tool_calls_per_step = 50

[defaults]
provider = "codex"            # use ChatGPT subscription by default
smart_provider = "gemini"     # for router solver escalation
cheap_provider = "ollama"     # for router solver discovery
confirm_tools = "dangerous"   # always | dangerous | never
budget_steps = 50
budget_tokens = 100000
budget_cost_usd = 0.0
tool_timeout_ms = 30000
max_tool_calls_per_step = 50
context_limit = 80000        # estimated token threshold; compress history when exceeded (0 = disabled)

[sandbox]
enabled = false
backend = "host"            # host | docker
image = "google/gemini-v100-research:latest"
network_tier = "off"        # off | research | open
memory_mb = 512
cpus = 1.0
apply_back = "manual"       # manual | on_success | never
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
	ensureString(&cfg.Tools.Enabled, "sh")
	ensureString(&cfg.Tools.Dangerous, "sh")
	ensureString(&cfg.Tools.Enabled, "git_push")
	ensureString(&cfg.Tools.Dangerous, "git_push")
	ensureString(&cfg.Tools.Enabled, "curl_fetch")
	ensureString(&cfg.Tools.Enabled, "agent")
	ensureString(&cfg.Tools.Dangerous, "agent")
	ensureString(&cfg.Tools.Enabled, "dispatch")
	ensureString(&cfg.Tools.Dangerous, "dispatch")
	ensureString(&cfg.Tools.Enabled, "orchestrate")
	ensureString(&cfg.Tools.Dangerous, "orchestrate")
	ensureString(&cfg.Tools.Enabled, "blackboard_read")
	ensureString(&cfg.Tools.Enabled, "blackboard_write")
	ensureString(&cfg.Tools.Dangerous, "blackboard_write")
	ensureString(&cfg.Tools.Enabled, "sem_diff")
	ensureString(&cfg.Tools.Enabled, "sem_impact")
	ensureString(&cfg.Tools.Enabled, "sem_blame")
	ensureString(&cfg.Tools.Enabled, "inspect_tool")
	if len(cfg.Agents) == 0 {
		cfg.Agents = DefaultConfig().Agents
	}
	if cfg.Defaults.SmartProvider == "" {
		cfg.Defaults.SmartProvider = DefaultConfig().Defaults.SmartProvider
	}
	if cfg.Defaults.CheapProvider == "" {
		cfg.Defaults.CheapProvider = DefaultConfig().Defaults.CheapProvider
	}
	applySandboxDefaults(&cfg.Sandbox, DefaultConfig().Sandbox)
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

func applySandboxDefaults(dst *SandboxConfig, defaults SandboxConfig) {
	if strings.TrimSpace(dst.Backend) == "" {
		dst.Backend = defaults.Backend
	}
	if strings.TrimSpace(dst.Image) == "" {
		dst.Image = defaults.Image
	}
	if strings.TrimSpace(dst.NetworkTier) == "" {
		dst.NetworkTier = defaults.NetworkTier
	}
	if dst.MemoryMB <= 0 {
		dst.MemoryMB = defaults.MemoryMB
	}
	if dst.CPUs <= 0 {
		dst.CPUs = defaults.CPUs
	}
	if strings.TrimSpace(dst.ApplyBack) == "" {
		dst.ApplyBack = defaults.ApplyBack
	}
}

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Provider != "glm" {
		t.Errorf("expected default provider glm, got %s", cfg.Defaults.Provider)
	}
	if cfg.Defaults.CheapProvider != "glm" {
		t.Errorf("expected default cheap provider glm, got %s", cfg.Defaults.CheapProvider)
	}
	if cfg.Defaults.CompressProvider != "glm" {
		t.Errorf("expected default compress provider glm, got %s", cfg.Defaults.CompressProvider)
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
	if cfg.UI.Theme != "v100" {
		t.Errorf("expected default UI theme v100, got %q", cfg.UI.Theme)
	}
	if !containsString(cfg.Tools.Env.Redact, "*_TOKEN") {
		t.Error("expected default tool env redaction patterns")
	}
	if cfg.Telegram.Enabled {
		t.Error("expected telegram default to be disabled")
	}
	if cfg.Telegram.BotTokenEnv != "V100_TELEGRAM_BOT_TOKEN" {
		t.Errorf("expected default telegram token env V100_TELEGRAM_BOT_TOKEN, got %q", cfg.Telegram.BotTokenEnv)
	}
	if cfg.Signal.Enabled {
		t.Error("expected signal default to be disabled")
	}
	if cfg.Signal.RPCMode != "socket" || cfg.Signal.Socket != "/run/signal-cli.sock" {
		t.Errorf("unexpected signal defaults: %+v", cfg.Signal)
	}
	if cfg.Tools.Auth.GitHub.Mode != "disabled" {
		t.Errorf("expected GitHub tool auth disabled by default, got %q", cfg.Tools.Auth.GitHub.Mode)
	}
	if cfg.Tools.Auth.GitHub.Env != "GH_TOKEN" {
		t.Errorf("expected GitHub tool auth env GH_TOKEN, got %q", cfg.Tools.Auth.GitHub.Env)
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
	if _, ok := cfg.Providers["claude"]; !ok {
		t.Error("expected claude provider alias in defaults")
	}
	if cfg.Providers["claude"].Type != "anthropic" {
		t.Errorf("expected claude provider alias to use anthropic type, got %s", cfg.Providers["claude"].Type)
	}
	if cfg.Providers["claude"].DefaultModel != "claude-opus-4-7" {
		t.Errorf("expected claude-opus-4-7, got %s", cfg.Providers["claude"].DefaultModel)
	}
	if cfg.Providers["claude"].Auth.Env != "ANTHROPIC_API_KEY" {
		t.Errorf("expected claude alias to use ANTHROPIC_API_KEY, got %s", cfg.Providers["claude"].Auth.Env)
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

	// Check that llamacpp provider exists in defaults
	if _, ok := cfg.Providers["llamacpp"]; !ok {
		t.Error("expected llamacpp provider in defaults")
	}
	if cfg.Providers["llamacpp"].Type != "llamacpp" {
		t.Errorf("expected type llamacpp, got %s", cfg.Providers["llamacpp"].Type)
	}
	if cfg.Providers["llamacpp"].DefaultModel != "gemma-4-E2B-it-GGUF:Q8_0" {
		t.Errorf("expected gemma-4-E2B-it-GGUF:Q8_0, got %s", cfg.Providers["llamacpp"].DefaultModel)
	}

	// Check that glm provider exists in defaults
	if _, ok := cfg.Providers["glm"]; !ok {
		t.Error("expected glm provider in defaults")
	}
	if cfg.Providers["glm"].Type != "glm" {
		t.Errorf("expected type glm, got %s", cfg.Providers["glm"].Type)
	}
	if cfg.Providers["glm"].DefaultModel != "GLM-5.1" {
		t.Errorf("expected GLM-5.1, got %s", cfg.Providers["glm"].DefaultModel)
	}
	if cfg.Providers["glm"].BaseURL != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("expected https://api.z.ai/api/coding/paas/v4, got %s", cfg.Providers["glm"].BaseURL)
	}
	if cfg.Providers["glm"].Auth.Env != "ZHIPU_API_KEY" {
		t.Errorf("expected ZHIPU_API_KEY, got %s", cfg.Providers["glm"].Auth.Env)
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
	if !containsString(cfg.Tools.Enabled, "deep_research") {
		t.Error("expected deep_research tool to be enabled by default")
	}
	if !containsString(cfg.Tools.Enabled, "source_code") {
		t.Error("expected source_code tool to be enabled by default")
	}
	if !containsString(cfg.Tools.Enabled, "translate") {
		t.Error("expected translate tool to be enabled by default")
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

[providers.llamacpp]
type = "llamacpp"
default_model = "gemma-4-E2B-it-GGUF:Q8_0"
base_url = "http://127.0.0.1:19091/v1"

[providers.anthropic]
type = "anthropic"
default_model = "claude-sonnet-4-20250514"
[providers.anthropic.auth]
env = "ANTHROPIC_API_KEY"

[tools]
enabled = ["fs_read"]
dangerous = []
[tools.env]
allow = ["GH_TOKEN"]
[tools.auth.github]
mode = "env"
env = "GITHUB_TOKEN"

[defaults]
provider = "anthropic"
budget_steps = 25

[ui]
theme = "dracula"
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
	if cfg.UI.Theme != "dracula" {
		t.Errorf("expected UI theme dracula, got %q", cfg.UI.Theme)
	}
	if !containsString(cfg.Tools.Env.Allow, "GH_TOKEN") {
		t.Errorf("expected tool env allow to include GH_TOKEN, got %v", cfg.Tools.Env.Allow)
	}
	if cfg.Tools.Auth.GitHub.Mode != "env" || cfg.Tools.Auth.GitHub.Env != "GITHUB_TOKEN" {
		t.Errorf("unexpected GitHub tool auth config: %+v", cfg.Tools.Auth.GitHub)
	}

	profilePath := filepath.Join(dir, "profiles.toml")
	if err := os.WriteFile(profilePath, []byte(`
[gateway.profiles.news_fr]
tools = ["news_fetch", "translate"]
dangerous = []
provider = "glm"
model = "glm-4.6"
solver = "react"
system_prompt = "Réponds en français."
network_tier = "research"
budget_steps = 12
budget_tokens = 40000
budget_cost_usd = 0.25
allowed_commands = ["help", "reset"]

[gateway.profiles.operator]
tools = ["fs_read", "sh"]
dangerous = ["sh"]
allowed_commands = ["help", "model", "provider", "solver", "profile", "reset"]

[telegram]
profile = "operator"

[telegram.chat_profiles]
"123456789" = "news_fr"

[signal]
profile = "news_fr"
account = "+15145551234"
rpc_mode = "tcp"
tcp = "127.0.0.1:7583"
allowed_numbers = ["+15145550000"]

[signal.chat_profiles]
"+15145550000" = "operator"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	profileCfg, err := Load(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	news := profileCfg.Gateway.Profiles["news_fr"]
	if news.Provider != "glm" || news.Model != "glm-4.6" || news.Solver != "react" {
		t.Fatalf("news profile runtime = %+v", news)
	}
	if !containsString(news.Tools, "translate") || len(news.Dangerous) != 0 {
		t.Fatalf("news profile tools/dangerous = %+v", news)
	}
	if news.SystemPrompt != "Réponds en français." || news.NetworkTier != "research" {
		t.Fatalf("news profile prompt/network = %+v", news)
	}
	if news.BudgetSteps != 12 || news.BudgetTokens != 40000 || news.BudgetCostUSD != 0.25 {
		t.Fatalf("news profile budgets = %+v", news)
	}
	if profileCfg.Telegram.Profile != "operator" || profileCfg.Telegram.ChatProfiles["123456789"] != "news_fr" {
		t.Fatalf("telegram profile binding = %+v", profileCfg.Telegram)
	}
	if profileCfg.Signal.Profile != "news_fr" || profileCfg.Signal.ChatProfiles["+15145550000"] != "operator" {
		t.Fatalf("signal profile binding = %+v", profileCfg.Signal)
	}
	if profileCfg.Signal.Account != "+15145551234" || profileCfg.Signal.RPCMode != "tcp" || profileCfg.Signal.TCP != "127.0.0.1:7583" {
		t.Fatalf("signal runtime config = %+v", profileCfg.Signal)
	}
	if profileCfg.ATProto.Handle != "" || profileCfg.ATProto.AppPasswordEnv != "" {
		t.Fatalf("unexpected atproto defaults in inline config test: %+v", profileCfg.ATProto)
	}
	name, resolved, ok := profileCfg.ResolveGatewayProfile(profileCfg.Telegram.Profile, profileCfg.Telegram.ChatProfiles, "123456789")
	if !ok || name != "news_fr" || resolved.SystemPrompt != "Réponds en français." {
		t.Fatalf("ResolveGatewayProfile chat override = %q %#v %v", name, resolved, ok)
	}
	name, resolved, ok = profileCfg.ResolveGatewayProfile(profileCfg.Telegram.Profile, profileCfg.Telegram.ChatProfiles, "999")
	if !ok || name != "operator" || !containsString(resolved.Tools, "sh") {
		t.Fatalf("ResolveGatewayProfile default = %q %#v %v", name, resolved, ok)
	}

	cfgEnvPath := filepath.Join(dir, "telegram_env.toml")
	t.Setenv("V100_TELEGRAM_TOKEN_TEST", "env-token")
	if err := os.WriteFile(cfgEnvPath, []byte(`
[telegram]
enabled = true
bot_token = ""
bot_token_env = "V100_TELEGRAM_TOKEN_TEST"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgFromEnv, err := Load(cfgEnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfgFromEnv.Telegram.Enabled {
		t.Fatal("expected telegram enabled when explicitly configured, even without inline bot_token")
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
	if !containsString(cfg.Tools.Enabled, "deep_research") {
		t.Error("expected deep_research tool to be enabled after migration")
	}
	if !containsString(cfg.Tools.Enabled, "source_code") {
		t.Error("expected source_code tool to be enabled after migration")
	}
	if !containsString(cfg.Tools.Enabled, "translate") {
		t.Error("expected translate tool to be enabled after migration")
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
	if !contains(toml, "[providers.claude]") {
		t.Error("default TOML should contain claude provider alias section")
	}
	if !contains(toml, `default_model = "claude-opus-4-7"`) {
		t.Error("default TOML should reference claude-opus-4-7")
	}
	if !contains(toml, "[providers.minimax]") {
		t.Error("default TOML should contain minimax provider section")
	}
	if !contains(toml, "MiniMax-M2.7") {
		t.Error("default TOML should reference MiniMax-M2.7 model")
	}
	if !contains(toml, "[providers.llamacpp]") {
		t.Error("default TOML should contain llamacpp provider section")
	}
	if !contains(toml, "gemma-4-E2B-it-GGUF:Q8_0") {
		t.Error("default TOML should reference the llamacpp default model")
	}
	if !contains(toml, "[providers.glm]") {
		t.Error("default TOML should contain glm provider section")
	}
	if !contains(toml, "GLM-5.1") {
		t.Error("default TOML should reference GLM-5.1 model")
	}
	if !contains(toml, "https://api.z.ai/api/coding/paas/v4") {
		t.Error("default TOML should reference z.ai coding API URL")
	}
	if !contains(toml, "ZHIPU_API_KEY") {
		t.Error("default TOML should reference ZHIPU_API_KEY")
	}
	if !contains(toml, "cheap_provider = \"glm\"") {
		t.Error("default TOML should default cheap provider to glm")
	}
	if !contains(toml, "compress_provider = \"glm\"") {
		t.Error("default TOML should default compression provider to glm")
	}
	if !contains(toml, "[ui]") || !contains(toml, `theme = "v100"`) {
		t.Error("default TOML should contain UI theme config")
	}
	if !contains(toml, "[tools.env]") || !contains(toml, `allow = []`) {
		t.Error("default TOML should contain tool env passthrough config")
	}
	if !contains(toml, "[tools.auth.github]") || !contains(toml, `mode = "disabled"`) {
		t.Error("default TOML should contain GitHub tool auth config")
	}
	if !contains(toml, "[sandbox]") {
		t.Error("default TOML should contain sandbox section")
	}
	if !contains(toml, `network_tier = "off"`) {
		t.Error("default TOML should default sandbox network_tier to off")
	}
	if !contains(toml, `image = "google/v100-agent-runtime:latest"`) {
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
	if !contains(toml, "[telegram]") {
		t.Error("default TOML should contain telegram config section")
	}
	if !contains(toml, `bot_token_env = "V100_TELEGRAM_BOT_TOKEN"`) {
		t.Error("default TOML should include telegram bot token env fallback")
	}
	if !contains(toml, "[signal]") {
		t.Error("default TOML should contain signal config section")
	}
	if !contains(toml, `rpc_mode = "socket"`) {
		t.Error("default TOML should include signal rpc mode")
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

func TestSignalVincentPresetIsReadOnlyAndResolvesPrompt(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "examples", "signal-chat-fr", "config.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	result := ValidateConfigPath(path)
	if result.HasErrors() {
		t.Fatalf("preset validation failed: %+v", result.Findings)
	}
	name, profile, ok := cfg.ResolveGatewayProfile(cfg.Signal.Profile, cfg.Signal.ChatProfiles, "+1XXXXXXXXXX")
	if !ok || name != "signal-vincent" {
		t.Fatalf("resolved profile = %q ok=%v", name, ok)
	}
	wantTools := []string{"web_search", "web_extract", "wiki", "translate", "atproto_feed", "atproto_notifications", "atproto_resolve"}
	if strings.Join(profile.Tools, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("tools = %v, want %v", profile.Tools, wantTools)
	}
	if len(profile.Dangerous) != 0 {
		t.Fatalf("dangerous = %v, want empty", profile.Dangerous)
	}
	for _, denied := range []string{"sh", "git_status", "git_commit", "git_push", "atproto_post", "atproto_create_record", "news_fetch"} {
		if containsString(profile.Tools, denied) {
			t.Fatalf("unsafe tool %q unexpectedly present in signal-vincent profile: %v", denied, profile.Tools)
		}
	}
	if profile.SystemPromptPath != "system_prompt_fr.md" {
		t.Fatalf("system_prompt_path = %q", profile.SystemPromptPath)
	}
	if cfg.ATProto.Handle != "your-handle.bsky.social" || cfg.ATProto.AppPasswordEnv != "V100_BSKY_APP_PASSWORD" {
		t.Fatalf("atproto config = %+v", cfg.ATProto)
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

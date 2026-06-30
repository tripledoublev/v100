package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigPathBehaviorDirsAndReferences(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.toml"), `[defaults]
provider = "codex"

[wake]
task = "nightly"
`)
	writeFile(t, filepath.Join(dir, "agents", "coder.md"), "coder prompt")
	writeFile(t, filepath.Join(dir, "agents", "coder.toml"), `system_prompt_path = "coder.md"`)
	writeFile(t, filepath.Join(dir, "policies", "review.md"), "review prompt")
	writeFile(t, filepath.Join(dir, "tasks", "nightly", "step.md"), "step prompt")
	writeFile(t, filepath.Join(dir, "tasks", "nightly", "task.toml"), `[[steps]]
name = "read"
prompt_path = "step.md"
`)

	result := ValidateConfigPath(filepath.Join(dir, "config.toml"))
	if result.HasErrors() {
		t.Fatalf("ValidateConfigPath() errors: %+v", result.Findings)
	}
	for _, want := range []string{
		"behavior dir agents/: 1 agent definition(s)",
		"behavior dir policies/: 1 policy definition(s)",
		"behavior dir tasks/: 1 task definition(s)",
	} {
		if !hasFinding(result, ValidationInfo, want) {
			t.Fatalf("missing info %q in %+v", want, result.Findings)
		}
	}
}

func TestValidateConfigPathMalformedRootTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, "TOML syntax error in config") {
		t.Fatalf("missing malformed TOML error: %+v", result.Findings)
	}
}

func TestValidateConfigPathMissingBehaviorDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"
`)

	result := ValidateConfigPath(path)
	if result.HasErrors() {
		t.Fatalf("missing behavior dirs should not be errors: %+v", result.Findings)
	}
	for _, want := range []string{
		"behavior dir agents/ not present",
		"behavior dir policies/ not present",
		"behavior dir tasks/ not present",
	} {
		if !hasFinding(result, ValidationInfo, want) {
			t.Fatalf("missing info %q in %+v", want, result.Findings)
		}
	}
}

func TestValidateConfigPathWarnsUnusedAndDeprecatedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"
max_tool_calls = 3

[unused]
value = true
`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationWarning, `deprecated config key "defaults.max_tool_calls"`) {
		t.Fatalf("missing deprecated warning: %+v", result.Findings)
	}
	if !hasFinding(result, ValidationWarning, `unused config key or section "unused.value"`) {
		t.Fatalf("missing unused warning: %+v", result.Findings)
	}
}

func TestValidateConfigPathBehaviorDirSyntaxAndMissingPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"
`)
	writeFile(t, filepath.Join(dir, "agents", "bad.toml"), `system_prompt = `)
	writeFile(t, filepath.Join(dir, "policies", "missing.toml"), `system_prompt_path = "missing.md"`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, "TOML syntax error in agent definition") {
		t.Fatalf("missing behavior TOML error: %+v", result.Findings)
	}
	if !hasFinding(result, ValidationError, "system_prompt_path references missing file") {
		t.Fatalf("missing prompt-path error: %+v", result.Findings)
	}
}

func TestValidateConfigPathProviderReferencesAndCycles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[providers.a]
type = "openai"
fallbacks = ["b"]

[providers.b]
type = "openai"
fallbacks = ["a"]

[defaults]
provider = "missing"
`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, `references provider "missing"`) {
		t.Fatalf("missing provider reference error: %+v", result.Findings)
	}
	if !hasFinding(result, ValidationError, "provider fallback cycle") {
		t.Fatalf("missing provider cycle error: %+v", result.Findings)
	}
}

func TestValidateConfigPathWakeTaskReference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"

[wake]
task = "missing-task"
`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, `references task "missing-task"`) {
		t.Fatalf("missing wake task reference error: %+v", result.Findings)
	}
}

func TestValidateConfigPathToolCredentialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"

[tools.env]
allow = ["GH_TOKEN", "bad-name"]

[tools.auth.github]
mode = "gh_config"
env = "1TOKEN"
`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, `invalid environment variable name "bad-name"`) {
		t.Fatalf("missing invalid tools.env.allow error: %+v", result.Findings)
	}
	if !hasFinding(result, ValidationError, `unsupported GitHub auth mode "gh_config"`) {
		t.Fatalf("missing invalid GitHub auth mode error: %+v", result.Findings)
	}
}

func TestValidateConfigPathUITheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `[defaults]
provider = "codex"

[ui]
theme = "solarized"
`)

	result := ValidateConfigPath(path)
	if !hasFinding(result, ValidationError, `unsupported UI theme "solarized"`) {
		t.Fatalf("missing unsupported theme error: %+v", result.Findings)
	}
}

func TestValidateConfigPathGatewayProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, path, `
[defaults]
provider = "codex"

[gateway.profiles.news_fr]
tools = ["news_fetch", "not_a_tool"]
dangerous = ["sh"]
system_prompt_path = "missing.md"

[telegram]
profile = "missing_default"

[telegram.chat_profiles]
"123" = "missing_chat"

[signal]
enabled = true
profile = "missing_signal"
account = ""
rpc_mode = "bogus"

[signal.chat_profiles]
"+15145550000" = "missing_signal_chat"
`)

	result := ValidateConfigPath(path)
	for _, needle := range []string{
		`unknown gateway profile "missing_default"`,
		`unknown gateway profile "missing_chat"`,
		`unknown gateway profile "missing_signal"`,
		`unknown gateway profile "missing_signal_chat"`,
		`signal gateway is enabled but account is empty`,
		`unsupported signal rpc_mode "bogus"`,
		`unknown tool "not_a_tool"`,
		`dangerous tool "sh" is not listed in tools`,
		`system_prompt_path references missing file`,
	} {
		if !hasFinding(result, ValidationError, needle) {
			t.Fatalf("missing gateway profile validation %q: %+v", needle, result.Findings)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFinding(result *ValidationResult, severity ValidationSeverity, needle string) bool {
	for _, finding := range result.Findings {
		if finding.Severity == severity && strings.Contains(finding.Message, needle) {
			return true
		}
	}
	return false
}

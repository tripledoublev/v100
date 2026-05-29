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

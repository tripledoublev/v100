package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestIsCompliantAgentHandoff(t *testing.T) {
	valid := strings.Repeat("x", 90) + `
## Summary
Short summary.

## Findings
- [P1] Something important.

## Next Steps
1. Do thing.
`
	if !isCompliantAgentHandoff("", valid) {
		t.Fatalf("expected valid handoff to be compliant")
	}

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "too short", in: "## Summary\nx\n## Findings\nx\n## Next Steps\nx"},
		{name: "missing findings", in: strings.Repeat("x", 100) + "\n## Summary\nx\n## Next Steps\n1. x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isCompliantAgentHandoff("", tc.in) {
				t.Fatalf("expected non-compliant handoff for case %q", tc.name)
			}
		})
	}

	researcher := strings.Repeat("x", 90) + `
## Summary
short
## Key Files
- cmd/v100/main.go — replay wiring
## Findings
- deterministic replay in replayCmd
## Next Steps
1. add tests
`
	if !isCompliantAgentHandoff("researcher", researcher) {
		t.Fatalf("expected researcher handoff to be compliant")
	}
}

func TestBuildSubAgentTask(t *testing.T) {
	first := buildSubAgentTask("", "analyze codebase", "", 1)
	if !strings.Contains(first, "analyze codebase") {
		t.Fatalf("first attempt prompt missing task")
	}
	if strings.Contains(first, "Your previous response was not compliant or empty.") {
		t.Fatalf("first attempt prompt should not include retry guidance")
	}
	if !strings.Contains(first, "## Summary") || !strings.Contains(first, "## Findings") || !strings.Contains(first, "## Next Steps") {
		t.Fatalf("first attempt prompt missing output contract")
	}

	second := buildSubAgentTask("", "analyze codebase", "bad output", 2)
	if !strings.Contains(second, "Your previous response was not compliant or empty.") {
		t.Fatalf("retry prompt missing retry guidance")
	}
	if !strings.Contains(second, "Previous output:\nbad output") {
		t.Fatalf("retry prompt missing previous output context")
	}

	researcher := buildSubAgentTask("researcher", "find replay files", "", 1)
	if !strings.Contains(researcher, "## Key Files") {
		t.Fatalf("researcher contract should include key files section")
	}
}

func TestExtractLastAssistantText(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first"},
		{Role: "assistant", Content: "   "},
		{Role: "tool", Content: "ignored"},
		{Role: "assistant", Content: "last answer"},
	}
	got := extractLastAssistantText(msgs)
	if got != "last answer" {
		t.Fatalf("unexpected last assistant text: %q", got)
	}

	if v := extractLastAssistantText(nil); v != "" {
		t.Fatalf("expected empty from nil messages, got %q", v)
	}
}

func TestParseInjectedToolOutputs(t *testing.T) {
	m, err := parseInjectedToolOutputs([]string{"project_search=parser.go:42", "fs_read=mocked file"})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := m["project_search"]; got != "parser.go:42" {
		t.Fatalf("unexpected project_search value: %q", got)
	}
	if got := m["fs_read"]; got != "mocked file" {
		t.Fatalf("unexpected fs_read value: %q", got)
	}

	if _, err := parseInjectedToolOutputs([]string{"bad-format"}); err == nil {
		t.Fatalf("expected parse error for missing '='")
	}
}

func TestApplyInjectedToolOutputs(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "find parser"},
		{Role: "tool", Name: "project_search", Content: "old"},
		{Role: "tool", Name: "fs_read", Content: "keep"},
	}
	injected := map[string]string{"project_search": "new-value"}
	got := applyInjectedToolOutputs(msgs, injected)
	if got[1].Content != "new-value" {
		t.Fatalf("expected injected tool content, got %q", got[1].Content)
	}
	if got[2].Content != "keep" {
		t.Fatalf("expected untouched tool content, got %q", got[2].Content)
	}
	if msgs[1].Content != "old" {
		t.Fatalf("input slice should not be mutated")
	}
}

func TestNormalizeModelOverride(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		in           string
		want         string
		wantChanged  bool
	}{
		{name: "codex 4o mini remapped", providerType: "codex", in: "gpt-4o-mini", want: "gpt-5.4", wantChanged: true},
		{name: "codex 4o remapped", providerType: "codex", in: "gpt-4o", want: "gpt-5.4", wantChanged: true},
		{name: "codex old codex remapped", providerType: "codex", in: "gpt-5.3-codex", want: "gpt-5.4", wantChanged: true},
		{name: "codex keep gpt5", providerType: "codex", in: "gpt-5.4", want: "gpt-5.4", wantChanged: false},
		{name: "openai keep 4o mini", providerType: "openai", in: "gpt-4o-mini", want: "gpt-4o-mini", wantChanged: false},
		{name: "blank unchanged", providerType: "codex", in: " ", want: "", wantChanged: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := normalizeModelOverride(tt.providerType, tt.in)
			if got != tt.want || changed != tt.wantChanged {
				t.Fatalf("normalizeModelOverride(%q, %q) = (%q,%v), want (%q,%v)",
					tt.providerType, tt.in, got, changed, tt.want, tt.wantChanged)
			}
		})
	}
}

func TestNormalizedProviderConfig(t *testing.T) {
	pc := normalizedProviderConfig(config.ProviderConfig{
		Type:         "codex",
		DefaultModel: "gpt-5.3-codex",
	})
	if pc.DefaultModel != "gpt-5.4" {
		t.Fatalf("normalizedProviderConfig default model = %q, want %q", pc.DefaultModel, "gpt-5.4")
	}

	other := normalizedProviderConfig(config.ProviderConfig{
		Type:         "openai",
		DefaultModel: "gpt-4o",
	})
	if other.DefaultModel != "gpt-4o" {
		t.Fatalf("normalizedProviderConfig should leave non-codex models unchanged, got %q", other.DefaultModel)
	}
}

func TestValidateExecutionSafety(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sandbox.Enabled = false

	if err := validateExecutionSafety(cfg, "dangerous", false); err != nil {
		t.Fatalf("dangerous confirm mode should be allowed, got %v", err)
	}
	if err := validateExecutionSafety(cfg, "never", true); err != nil {
		t.Fatalf("unsafe host execution should be allowed explicitly, got %v", err)
	}
	if err := validateExecutionSafety(cfg, "never", false); err == nil {
		t.Fatal("expected host auto-approval without sandbox to be rejected")
	}
}

func TestLoadConfigAddsDefaultProviders(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
[providers.codex]
type = "codex"
default_model = "gpt-5.4"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "gemini", "minimax", "anthropic", "openai", "ollama"} {
		if _, ok := cfg.Providers[name]; !ok {
			t.Fatalf("expected provider %q to be present after load", name)
		}
	}
}

func TestFindRunDirFindsNestedRun(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "runs", "bench", "run-123")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "trace.jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := withWorkingDir(root, func() error {
		got, err := findRunDir("run-123")
		if err != nil {
			return err
		}
		if got != filepath.Join("runs", "bench", "run-123") {
			t.Fatalf("findRunDir returned %q", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEnabledToolSummary(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "fs_write", "sh"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.Sh())

	got := enabledToolSummary(reg)
	for _, want := range []string{"3 enabled", "2 dangerous", "fs_read", "fs_write", "sh"} {
		if !strings.Contains(got, want) {
			t.Fatalf("enabledToolSummary missing %q in %q", want, got)
		}
	}
}

func TestBuildToolRegistryDefaultConfigValidates(t *testing.T) {
	cfg := config.DefaultConfig()
	reg := buildToolRegistry(cfg)
	if err := reg.Validate(); err != nil {
		t.Fatalf("default tool registry should validate, got %v", err)
	}
}

func TestBuildToolRegistryPartialConfigValidatesFails(t *testing.T) {
	cfg := config.DefaultConfig()
	// Add a tool name that will never be registered
	cfg.Tools.Enabled = append(cfg.Tools.Enabled, "nonexistent_tool")
	reg := buildToolRegistry(cfg)
	if err := reg.Validate(); err == nil {
		t.Fatal("expected Validate to fail for unregistered enabled tool, got nil")
	}
}

package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestDefault(t *testing.T) {
	p := Default()
	if p.Name != "default" {
		t.Errorf("expected name 'default', got %s", p.Name)
	}
	if p.SystemPrompt == "" {
		t.Error("expected non-empty system prompt")
	}
	if p.MaxToolCallsPerStep != 20 {
		t.Errorf("expected 20 max tool calls, got %d", p.MaxToolCallsPerStep)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "test.md")
	if err := os.WriteFile(promptFile, []byte("custom prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load("test", config.PolicyConfig{
		SystemPromptPath:    promptFile,
		MaxToolCallsPerStep: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemPrompt != "custom prompt" {
		t.Errorf("expected custom prompt, got %s", p.SystemPrompt)
	}
	if p.MaxToolCallsPerStep != 30 {
		t.Errorf("expected 30, got %d", p.MaxToolCallsPerStep)
	}
}

func TestLoadFallbackToDefault(t *testing.T) {
	p, err := Load("missing", config.PolicyConfig{
		SystemPromptPath: "/nonexistent/path/to/prompt.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemPrompt != DefaultSystemPrompt {
		t.Error("expected fallback to default prompt")
	}
}

func TestLoadDefaultMaxToolCalls(t *testing.T) {
	p, err := Load("zero", config.PolicyConfig{
		MaxToolCallsPerStep: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxToolCallsPerStep != 20 {
		t.Errorf("expected default 20, got %d", p.MaxToolCallsPerStep)
	}
}

package policy

import (
	"os"
	"path/filepath"
	"strings"
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
	if !strings.Contains(p.SystemPrompt, "shell tool can download network resources") {
		t.Error("expected default system prompt to disclose shell download capability")
	}
	if !strings.Contains(p.SystemPrompt, "save files into the workspace") {
		t.Error("expected default system prompt to disclose workspace-save constraint")
	}
	if !strings.Contains(p.SystemPrompt, "Do not claim you cannot see images") {
		t.Error("expected default system prompt to include direct image-inspection guidance")
	}
	if !strings.Contains(p.SystemPrompt, "minimal sanitized environment") {
		t.Error("expected default system prompt to disclose sanitized shell environment")
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

func TestLoadMigratesLegacyDefaultPrompt(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "default.md")
	if err := os.WriteFile(promptFile, []byte(LegacyDefaultSystemPrompt), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load("default", config.PolicyConfig{
		SystemPromptPath: promptFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemPrompt != DefaultSystemPrompt {
		t.Fatal("expected legacy prompt to migrate to current default")
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != DefaultSystemPrompt {
		t.Fatal("expected migrated prompt to be written back to disk")
	}
}

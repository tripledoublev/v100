package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentConfig_ResolvePrompt_Inline(t *testing.T) {
	agent := AgentConfig{SystemPrompt: "inline prompt"}
	got, err := agent.ResolvePrompt("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "inline prompt" {
		t.Errorf("expected inline prompt, got %q", got)
	}
}

func TestAgentConfig_ResolvePrompt_FromPath(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(promptPath, []byte("file prompt content"), 0644); err != nil {
		t.Fatal(err)
	}
	agent := AgentConfig{SystemPromptPath: "agent.md"}
	got, err := agent.ResolvePrompt(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file prompt content" {
		t.Errorf("expected file content, got %q", got)
	}
}

func TestAgentConfig_ResolvePrompt_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(promptPath, []byte("absolute"), 0644); err != nil {
		t.Fatal(err)
	}
	agent := AgentConfig{SystemPromptPath: promptPath}
	got, err := agent.ResolvePrompt("/some/other/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "absolute" {
		t.Errorf("expected absolute, got %q", got)
	}
}

func TestAgentConfig_ResolvePrompt_MissingFile(t *testing.T) {
	agent := AgentConfig{SystemPromptPath: "nonexistent.md"}
	_, err := agent.ResolvePrompt(t.TempDir())
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestWakeTaskStep_ResolvePrompt_Inline(t *testing.T) {
	step := WakeTaskStep{Prompt: "inline step prompt"}
	got, err := step.ResolvePrompt("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "inline step prompt" {
		t.Errorf("expected inline, got %q", got)
	}
}

func TestWakeTaskStep_ResolvePrompt_FromPath(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "step.md")
	if err := os.WriteFile(promptPath, []byte("step from file"), 0644); err != nil {
		t.Fatal(err)
	}
	step := WakeTaskStep{PromptPath: "step.md"}
	got, err := step.ResolvePrompt(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "step from file" {
		t.Errorf("expected file content, got %q", got)
	}
}

func TestLoadAgentFile(t *testing.T) {
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "researcher.toml")
	content := `system_prompt = "You are a researcher"
tools = ["fs_read", "web_search"]
model = "glm-5.1"
budget_tokens = 50000
`
	if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	agent, err := LoadAgentFile(agentPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.SystemPrompt != "You are a researcher" {
		t.Errorf("wrong system prompt: %q", agent.SystemPrompt)
	}
	if len(agent.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(agent.Tools))
	}
	if agent.Model != "glm-5.1" {
		t.Errorf("wrong model: %q", agent.Model)
	}
	if agent.BudgetTokens != 50000 {
		t.Errorf("wrong budget: %d", agent.BudgetTokens)
	}
}

func TestLoadAgentFile_WithPromptPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("external prompt"), 0644); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(dir, "agent.toml")
	content := `system_prompt_path = "prompt.md"
tools = ["fs_read"]
`
	if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	agent, err := LoadAgentFile(agentPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompt, err := agent.ResolvePrompt(dir)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if prompt != "external prompt" {
		t.Errorf("expected external prompt, got %q", prompt)
	}
}

func TestLoadAgentsDirectory(t *testing.T) {
	dir := t.TempDir()

	// Write two valid agent files
	files := map[string]string{
		"researcher.toml": `system_prompt = "research"`,
		"coder.toml":      `system_prompt = "code"`,
		"readme.md":       `# This should be ignored`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	agents, err := LoadAgentsDirectory(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d: %v", len(agents), agents)
	}
	if agents["researcher"].SystemPrompt != "research" {
		t.Errorf("missing researcher")
	}
	if agents["coder"].SystemPrompt != "code" {
		t.Errorf("missing coder")
	}
}

func TestLoadAgentsDirectory_MissingDir(t *testing.T) {
	agents, err := LoadAgentsDirectory("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("expected no error for missing dir, got: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected empty map, got %d agents", len(agents))
	}
}

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

func TestConfigPromptBaseDirFromLoadedConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`[defaults]
provider = "codex"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.PromptBaseDir(); got != dir {
		t.Fatalf("PromptBaseDir() = %q, want %q", got, dir)
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
	prompt, err := agent.ResolvePrompt("")
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

func TestLoadTasksDirectorySupportsFilesAndTaskDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "single.toml"), []byte(`description = "single file"
[[steps]]
name = "read"
prompt = "inline"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(dir, "complex")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "draft.md"), []byte("prompt from task dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "task.toml"), []byte(`description = "directory task"
[[steps]]
name = "draft"
prompt_path = "draft.md"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks, err := LoadTasksDirectory(dir)
	if err != nil {
		t.Fatalf("LoadTasksDirectory() error = %v", err)
	}
	if tasks["single"].Name != "single" {
		t.Fatalf("single task name = %q, want single", tasks["single"].Name)
	}
	complex := tasks["complex"]
	if complex.Name != "complex" {
		t.Fatalf("complex task name = %q, want complex", complex.Name)
	}
	got, err := complex.Steps[0].ResolvePrompt(complex.PromptBaseDir(""))
	if err != nil {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
	if got != "prompt from task dir" {
		t.Fatalf("directory task prompt = %q", got)
	}
}

func TestLoadPoliciesDirectorySupportsMarkdownTomlAndDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "direct.md"), []byte("direct prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review.toml"), []byte(`system_prompt = "review prompt"
max_tool_calls_per_step = 12
`), 0o644); err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(dir, "complex")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "prompt.md"), []byte("complex prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "policy.toml"), []byte(`system_prompt_path = "prompt.md"`), 0o644); err != nil {
		t.Fatal(err)
	}

	policies, err := LoadPoliciesDirectory(dir)
	if err != nil {
		t.Fatalf("LoadPoliciesDirectory() error = %v", err)
	}
	directPrompt, err := policies["direct"].ResolvePrompt("")
	if err != nil {
		t.Fatalf("direct ResolvePrompt() error = %v", err)
	}
	if directPrompt != "direct prompt" {
		t.Fatalf("direct prompt = %q", directPrompt)
	}
	if policies["review"].MaxToolCallsPerStep != 12 {
		t.Fatalf("review max calls = %d, want 12", policies["review"].MaxToolCallsPerStep)
	}
	complexPrompt, err := policies["complex"].ResolvePrompt("")
	if err != nil {
		t.Fatalf("complex ResolvePrompt() error = %v", err)
	}
	if complexPrompt != "complex prompt" {
		t.Fatalf("complex prompt = %q", complexPrompt)
	}
}

func TestLoadPoliciesDirectoryLetsTomlOverrideSameNameMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "default.md"), []byte("default markdown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.toml"), []byte(`system_prompt_path = "default.md"
max_tool_calls_per_step = 77
`), 0o644); err != nil {
		t.Fatal(err)
	}

	policies, err := LoadPoliciesDirectory(dir)
	if err != nil {
		t.Fatalf("LoadPoliciesDirectory() error = %v", err)
	}
	if policies["default"].MaxToolCallsPerStep != 77 {
		t.Fatalf("MaxToolCallsPerStep = %d, want 77", policies["default"].MaxToolCallsPerStep)
	}
	got, err := policies["default"].ResolvePrompt("")
	if err != nil {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
	if got != "default markdown" {
		t.Fatalf("prompt = %q, want default markdown", got)
	}
}

func TestLoadMergesBehaviorDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`[defaults]
provider = "codex"

[agents.inline]
system_prompt = "inline"

[[wake.tasks]]
name = "inline-task"
[[wake.tasks.steps]]
name = "inline"
prompt = "inline task"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "file-agent.md"), []byte("agent prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "file-agent.toml"), []byte(`system_prompt_path = "file-agent.md"`), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(dir, "tasks", "file-task")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "step.md"), []byte("task prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "task.toml"), []byte(`[[steps]]
name = "step"
prompt_path = "step.md"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	policiesDir := filepath.Join(dir, "policies")
	if err := os.MkdirAll(policiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policiesDir, "file-policy.md"), []byte("policy prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agentPrompt, err := cfg.Agents["file-agent"].ResolvePrompt("")
	if err != nil {
		t.Fatalf("agent ResolvePrompt() error = %v", err)
	}
	if agentPrompt != "agent prompt" {
		t.Fatalf("agent prompt = %q", agentPrompt)
	}
	if cfg.Agents["inline"].SystemPrompt != "inline" {
		t.Fatal("inline agent should remain available")
	}
	fileTask := findTaskForTest(cfg.Wake.Tasks, "file-task")
	if fileTask == nil {
		t.Fatal("file-task not loaded")
	}
	taskPrompt, err := fileTask.Steps[0].ResolvePrompt(fileTask.PromptBaseDir(cfg.PromptBaseDir()))
	if err != nil {
		t.Fatalf("task ResolvePrompt() error = %v", err)
	}
	if taskPrompt != "task prompt" {
		t.Fatalf("task prompt = %q", taskPrompt)
	}
	if findTaskForTest(cfg.Wake.Tasks, "inline-task") == nil {
		t.Fatal("inline task should remain available")
	}
	policyPrompt, err := cfg.Policies["file-policy"].ResolvePrompt("")
	if err != nil {
		t.Fatalf("policy ResolvePrompt() error = %v", err)
	}
	if policyPrompt != "policy prompt" {
		t.Fatalf("policy prompt = %q", policyPrompt)
	}
}

func findTaskForTest(tasks []WakeTask, name string) *WakeTask {
	for i := range tasks {
		if tasks[i].Name == name {
			return &tasks[i]
		}
	}
	return nil
}

func TestAgentConfig_ResolvePrompt_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sub := filepath.Join(home, "promptdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "agent.md"), []byte("tilde works"), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := AgentConfig{SystemPromptPath: "~/promptdir/agent.md"}
	got, err := agent.ResolvePrompt("/some/other/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tilde works" {
		t.Errorf("expected tilde-expanded content, got %q", got)
	}
}

func TestWakeTaskStep_ResolvePrompt_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "step.md"), []byte("step tilde"), 0o644); err != nil {
		t.Fatal(err)
	}
	step := WakeTaskStep{PromptPath: "~/step.md"}
	got, err := step.ResolvePrompt("/elsewhere")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "step tilde" {
		t.Errorf("expected tilde-expanded content, got %q", got)
	}
}

func TestXDGConfigDir(t *testing.T) {
	got := XDGConfigDir()
	if got == "" {
		t.Error("XDGConfigDir returned empty string")
	}
	if filepath.Base(got) != "v100" {
		t.Errorf("expected last segment v100, got %q", got)
	}
}

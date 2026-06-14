package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestConfiguredAgentNamesSorted(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"reviewer": {},
			"coder":    {},
			"research": {},
		},
	}

	got := configuredAgentNames(cfg)
	want := []string{"coder", "research", "reviewer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("configuredAgentNames() = %v, want %v", got, want)
	}
}

func TestFormatUnknownAgentRoleIncludesGuidance(t *testing.T) {
	msg := formatUnknownAgentRole(config.DefaultConfig(), "default")
	for _, want := range []string{
		"unknown agent role: default",
		"available: coder, researcher, reviewer",
		"v100 agents",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("formatUnknownAgentRole() missing %q in %q", want, msg)
		}
	}
}

func TestAgentsCmdListsDefaultRoles(t *testing.T) {
	cfgPath := t.TempDir() + "/missing.toml"

	out, err := captureStdout(func() error {
		cmd := agentsCmd(&cfgPath)
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"coder", "researcher", "reviewer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agents output missing %q in:\n%s", want, out)
		}
	}
}

func TestAgentsCmdIncludesRunSubcommand(t *testing.T) {
	cfgPath := t.TempDir() + "/missing.toml"
	cmd := agentsCmd(&cfgPath)
	for _, name := range []string{"run", "cancel", "rerun"} {
		sub, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if sub == nil || sub.Name() != name {
			t.Fatalf("%s subcommand not found: %#v", name, sub)
		}
	}
	run, _, _ := cmd.Find([]string{"run"})
	flag := run.Flags().Lookup("handoff-schema-name")
	if flag == nil || flag.DefValue != tools.HandoffSchemaStandard {
		t.Fatalf("handoff schema flag = %#v", flag)
	}
}

func TestSplitAgentRunCSVTrimsDeduplicatesAndDropsEmpty(t *testing.T) {
	got := splitAgentRunCSV(" fs_read, sh,fs_read,, ")
	want := []string{"fs_read", "sh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("splitAgentRunCSV() = %v, want %v", got, want)
	}
}

func TestReadAgentRunHandoffSchemaValidatesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := readAgentRunHandoffSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"type":"object"}` {
		t.Fatalf("schema = %s", raw)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAgentRunHandoffSchema(bad); err == nil {
		t.Fatal("expected invalid JSON schema file to fail")
	}
}

func TestAgentRunCancelWritesMarker(t *testing.T) {
	runDir := t.TempDir()
	if err := requestAgentRunCancel(runDir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(agentRunCancelPath(filepath.Join(runDir, "state")))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["cancelled_at"] == "" {
		t.Fatalf("cancel marker missing timestamp: %s", raw)
	}
}

func TestResolveAgentRunDirAndLoadAgentStart(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteMeta(runDir, core.RunMeta{RunID: "run-1", SourceWorkspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	payload := core.AgentStartPayload{
		Agent:      "reviewer",
		AgentRunID: "agent-call-1",
		Task:       "review this",
		Model:      "glm:glm-4.6",
		Tools:      []string{"fs_read", "git_diff"},
		MaxSteps:   4,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   "run-1",
		EventID: "agent-start",
		Type:    core.EventAgentStart,
		Payload: raw,
	}); err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveAgentRunDir("run-1", root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != runDir {
		t.Fatalf("resolved run dir = %q, want %q", resolved, runDir)
	}
	start, err := loadAgentRunStart(runDir, "agent-call-1")
	if err != nil {
		t.Fatal(err)
	}
	var opts agentRunOptions
	fillAgentRerunOptions(&opts, runDir, start)
	if opts.Provider != "glm" || opts.Model != "glm-4.6" || opts.MaxSteps != 4 || opts.ToolsCSV != "fs_read,git_diff" || opts.Workspace != "/workspace" {
		t.Fatalf("rerun opts = %#v", opts)
	}
}

func TestAgentsCmdListsDirectoryRoles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`[defaults]
provider = "codex"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "critic.toml"), []byte(`system_prompt = "critic"
tools = ["fs_read"]
model = "glm"
budget_steps = 3
budget_tokens = 123
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(func() error {
		cmd := agentsCmd(&cfgPath)
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"critic", "model=glm", "tokens=123", "fs_read"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agents output missing %q in:\n%s", want, out)
		}
	}
}

func TestResolveAgentSystemPromptUsesConfigSourceDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("from file"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.SourceDir = dir
	role := config.AgentConfig{
		SystemPrompt:     "inline",
		SystemPromptPath: "agent.md",
	}

	got, err := resolveAgentSystemPrompt(cfg, role)
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt() error = %v", err)
	}
	if got != "from file" {
		t.Fatalf("resolveAgentSystemPrompt() = %q, want file prompt", got)
	}
}

func TestResolveAgentSystemPromptUsesAgentSourceDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("from agent dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.SourceDir = t.TempDir()
	role := config.AgentConfig{
		SourceDir:        dir,
		SystemPromptPath: "agent.md",
	}

	got, err := resolveAgentSystemPrompt(cfg, role)
	if err != nil {
		t.Fatalf("resolveAgentSystemPrompt() error = %v", err)
	}
	if got != "from agent dir" {
		t.Fatalf("resolveAgentSystemPrompt() = %q, want agent-dir prompt", got)
	}
}

func TestResolveAgentSystemPromptFailsOnMissingPromptPath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.SourceDir = t.TempDir()
	role := config.AgentConfig{
		SystemPrompt:     "inline",
		SystemPromptPath: "missing.md",
	}

	_, err := resolveAgentSystemPrompt(cfg, role)
	if err == nil {
		t.Fatal("expected error for missing system_prompt_path")
	}
}

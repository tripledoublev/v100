package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

type wakeTestProvider struct {
	response providers.CompleteResponse
	err      error
}

func (p *wakeTestProvider) Name() string { return "wake-test" }
func (p *wakeTestProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}
func (p *wakeTestProvider) Complete(_ context.Context, _ providers.CompleteRequest) (providers.CompleteResponse, error) {
	return p.response, p.err
}
func (p *wakeTestProvider) Embed(_ context.Context, _ providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (p *wakeTestProvider) Metadata(_ context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: model, ContextSize: 128000}, nil
}

func TestResolveWakeIntervalUsesConfigDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Wake.IntervalSeconds = 90

	got, err := resolveWakeInterval(cfg, false, "1h")
	if err != nil {
		t.Fatalf("resolveWakeInterval() error = %v", err)
	}
	if got != 90*time.Second {
		t.Fatalf("resolveWakeInterval() = %v, want %v", got, 90*time.Second)
	}
}

func TestResolveWakeIntervalUsesFlagOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Wake.IntervalSeconds = 90

	got, err := resolveWakeInterval(cfg, true, "2m")
	if err != nil {
		t.Fatalf("resolveWakeInterval() error = %v", err)
	}
	if got != 2*time.Minute {
		t.Fatalf("resolveWakeInterval() = %v, want %v", got, 2*time.Minute)
	}
}

func TestResolveWakeProviderPrefersFlagThenConfigThenDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.Provider = "minimax"
	cfg.Wake.Provider = "gemini"

	if got := resolveWakeProvider(cfg, "openai"); got != "openai" {
		t.Fatalf("flag override = %q, want openai", got)
	}
	if got := resolveWakeProvider(cfg, ""); got != "gemini" {
		t.Fatalf("wake config provider = %q, want gemini", got)
	}
	cfg.Wake.Provider = ""
	if got := resolveWakeProvider(cfg, ""); got != "minimax" {
		t.Fatalf("default provider = %q, want minimax", got)
	}
}

func TestWakeRunRequiresToken(t *testing.T) {
	cfgPath := ""
	cmd := wakeRunCmd(&cfgPath)
	if err := cmd.Flags().Set("state-path", filepath.Join(t.TempDir(), "wake.json")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("interval", "1s"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected wake run without token to fail")
	}
}

func TestBuildWakePromptIncludesWorkspaceEntries(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"README.md", "cmd", "internal", "runs"} {
		path := filepath.Join(workspace, name)
		if strings.Contains(name, ".") {
			if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	prompt, err := buildWakePrompt(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "README.md") || !strings.Contains(prompt, "cmd/") {
		t.Fatalf("prompt missing workspace entries: %q", prompt)
	}
	if strings.Contains(prompt, "runs/") {
		t.Fatalf("prompt should omit runs directory: %q", prompt)
	}
	if !strings.Contains(prompt, "GOAL: <one sentence>") || !strings.Contains(prompt, "WHY: <one sentence>") {
		t.Fatalf("prompt missing structured response format: %q", prompt)
	}
}

func TestExtractWakeGoalsParsesAssistantGoal(t *testing.T) {
	goals := extractWakeGoals([]providers.Message{
		{Role: "assistant", Content: "GOAL: Stabilize wake cycle startup reporting\nWHY: Startup lies are hard to debug."},
	})
	if len(goals) != 1 {
		t.Fatalf("len(goals) = %d, want 1", len(goals))
	}
	if goals[0].Content != "Stabilize wake cycle startup reporting" {
		t.Fatalf("goal content = %q", goals[0].Content)
	}
}

func TestCollectWakeWorkspaceSummaryIncludesNestedEntries(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "internal", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "internal", "core", "wake.go"), []byte("package core"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "runs", "old-run"), 0o755); err != nil {
		t.Fatal(err)
	}

	summary, err := collectWakeWorkspaceSummary(workspace, 2, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "internal/") || !strings.Contains(summary, "internal/core/") {
		t.Fatalf("summary missing nested entries: %q", summary)
	}
	if strings.Contains(summary, "runs/") {
		t.Fatalf("summary should omit runs paths: %q", summary)
	}
}

func TestParseWakeGoalFallsBackToFirstLine(t *testing.T) {
	got := parseWakeGoal("- Improve wake backoff observability\nwith extra detail")
	if got != "Improve wake backoff observability" {
		t.Fatalf("parseWakeGoal() = %q", got)
	}
}

func TestRunWakeCycleWithProviderCreatesRunArtifacts(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Defaults.Provider = "minimax"
	cfg.Providers["minimax"] = config.ProviderConfig{Type: "minimax", DefaultModel: "MiniMax-M2.7"}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}

	prov := &wakeTestProvider{
		response: providers.CompleteResponse{
			AssistantText: "GOAL: Review the top-level workspace layout and tighten wake lifecycle tests.\nWHY: Wake runs should prove they are producing durable artifacts.",
		},
	}

	runID, goals, err := runWakeCycleWithProvider(context.Background(), cfg, workspace, "minimax", prov)
	if err != nil {
		t.Fatalf("runWakeCycleWithProvider() error = %v", err)
	}
	if runID == "" {
		t.Fatal("expected run ID")
	}
	if len(goals) != 1 {
		t.Fatalf("len(goals) = %d, want 1", len(goals))
	}

	runDir := filepath.Join(workspace, "runs", runID)
	meta, err := core.ReadMeta(runDir)
	if err != nil {
		t.Fatalf("ReadMeta() error = %v", err)
	}
	if len(meta.GeneratedGoals) != 1 {
		t.Fatalf("meta.GeneratedGoals = %d, want 1", len(meta.GeneratedGoals))
	}

	events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var foundGoal, foundEnd bool
	for _, ev := range events {
		switch ev.Type {
		case core.EventGeneratedGoal:
			foundGoal = true
		case core.EventRunEnd:
			foundEnd = true
		}
	}
	if !foundGoal {
		t.Fatal("expected generated.goal event")
	}
	if !foundEnd {
		t.Fatal("expected run.end event")
	}
}

func TestWaitForWakeReadyObservesRunningState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "wake.json")
	token := "tok123"
	pid := 4321

	go func() {
		time.Sleep(100 * time.Millisecond)
		state := core.InitWakeState()
		state.Status = core.WakeStatusRunning
		state.PID = pid
		state.Token = token
		_ = core.WriteWakeState(statePath, state)
	}()

	if err := waitForWakeReady(statePath, pid, token, 2*time.Second); err != nil {
		t.Fatalf("waitForWakeReady() error = %v", err)
	}
}

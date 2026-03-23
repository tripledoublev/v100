package main

import (
	"context"
	"fmt"
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

type wakeExecCall struct {
	name  string
	args  []string
	stdin string
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

	prompt, err := buildWakePrompt(workspace, nil)
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

func TestBuildWakePromptUsesQueuedGoalWhenPresent(t *testing.T) {
	workspace := t.TempDir()
	goal := &core.GeneratedGoal{Content: "Improve wake failure summaries"}
	prompt, err := buildWakePrompt(workspace, goal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Prior queued goal:") || !strings.Contains(prompt, goal.Content) {
		t.Fatalf("prompt missing queued goal context: %q", prompt)
	}
}

func TestParseWakeGoalFallsBackToFirstLine(t *testing.T) {
	got := parseWakeGoal("- Improve wake backoff observability\nwith extra detail")
	if got != "Improve wake backoff observability" {
		t.Fatalf("parseWakeGoal() = %q", got)
	}
}

func TestDedupeWakeGoalsSkipsDuplicateContent(t *testing.T) {
	existing := []core.GeneratedGoal{{Content: "Improve wake startup reporting"}}
	incoming := []core.GeneratedGoal{
		{Content: "Improve wake startup reporting"},
		{Content: "Add queued-goal execution metrics"},
	}
	got := dedupeWakeGoals(existing, incoming)
	if len(got) != 1 {
		t.Fatalf("len(dedupeWakeGoals) = %d, want 1", len(got))
	}
	if got[0].Content != "Add queued-goal execution metrics" {
		t.Fatalf("deduped goal = %q", got[0].Content)
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

	runID, goals, err := runWakeCycleWithProvider(context.Background(), cfg, workspace, "minimax", prov, nil)
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

func TestRunWakeCycleWithQueuedGoalUsesQueuedContext(t *testing.T) {
	workspace := t.TempDir()
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
			AssistantText: "GOAL: Add a focused wake-cycle failure regression test.\nWHY: The queued goal should turn into an immediate concrete next step.",
		},
	}

	activeGoal := &core.GeneratedGoal{Content: "Harden wake failure handling"}
	_, goals, err := runWakeCycleWithProvider(context.Background(), cfg, workspace, "minimax", prov, activeGoal)
	if err != nil {
		t.Fatalf("runWakeCycleWithProvider() error = %v", err)
	}
	if len(goals) != 1 {
		t.Fatalf("len(goals) = %d, want 1", len(goals))
	}
	if goals[0].Content != "Add a focused wake-cycle failure regression test." {
		t.Fatalf("goal content = %q", goals[0].Content)
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

func TestExtractRunIDFromOutput(t *testing.T) {
	out := "trace: runs/abc/trace.jsonl\nrun id: 20260322T120000-deadbeef\n"
	if got := extractRunIDFromOutput(out); got != "20260322T120000-deadbeef" {
		t.Fatalf("extractRunIDFromOutput() = %q", got)
	}
}

func TestBuildWakeIssuePromptIncludesIssueWorkflow(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Wake.Objective = "Close open issues overnight."
	issue := wakeIssue{Number: 123, Title: "Fix wake daemon", Body: "Make it reliable."}

	prompt := buildWakeIssuePrompt(cfg, "/repo", issue)
	for _, want := range []string{
		"Close open issues overnight.",
		"Repository workspace: current repository root (repo)",
		"Selected GitHub issue: #123 Fix wake daemon",
		"./scripts/lint.sh",
		"env GOCACHE=.gocache go test ./...",
		"env GOCACHE=.gocache go build ./...",
		"Use relative paths like `cmd/v100/cmd_run.go` or `dogfood/verify_test.toml` with repo tools.",
		"Do not pass the absolute host workspace path to repo tools.",
		"Do not push and do not close the GitHub issue yourself; the daemon will handle that after verifying your commit.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "/repo/.gocache") {
		t.Fatalf("prompt should not contain host absolute verification paths: %q", prompt)
	}
}

func TestRunHeadlessIssueWorkerPassesWakeBudgetsAndToolCeiling(t *testing.T) {
	oldExec := wakeExecCommand
	defer func() { wakeExecCommand = oldExec }()

	var got wakeExecCall
	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		got = wakeExecCall{name: name, args: append([]string(nil), args...), stdin: stdin}
		return "run id: 20260322T120000-deadbeef\n", nil
	}

	cfg := config.DefaultConfig()
	cfg.Wake.BudgetSteps = 17
	cfg.Wake.BudgetTokens = 54321
	cfg.Defaults.MaxToolCallsPerStep = 37
	cfg.Policies["default"] = config.PolicyConfig{MaxToolCallsPerStep: 41}

	runID, err := runHeadlessIssueWorker(context.Background(), cfg, "/tmp/v100", "/tmp/cfg.toml", "prompt body", "codex")
	if err != nil {
		t.Fatalf("runHeadlessIssueWorker() error = %v", err)
	}
	if runID != "20260322T120000-deadbeef" {
		t.Fatalf("runID = %q", runID)
	}
	if got.name != "/tmp/v100" {
		t.Fatalf("command name = %q, want /tmp/v100", got.name)
	}
	joined := strings.Join(got.args, " ")
	for _, want := range []string{
		"--config /tmp/cfg.toml",
		"run",
		"--auto",
		"--unsafe",
		"--exit",
		"--sandbox",
		"--disable-watchdogs",
		"--provider codex",
		"--budget-steps 17",
		"--budget-tokens 54321",
		"--max-tool-calls-per-step 41",
		"--prompt-file -",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %q", want, joined)
		}
	}
	if got.stdin != "prompt body" {
		t.Fatalf("stdin = %q, want prompt body", got.stdin)
	}
}

func TestSelectWakeIssuePrefersCurrentOpenIssue(t *testing.T) {
	oldExec := wakeExecCommand
	defer func() { wakeExecCommand = oldExec }()

	var calls []wakeExecCall
	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		calls = append(calls, wakeExecCall{name: name, args: append([]string(nil), args...), stdin: stdin})
		if len(args) >= 3 && args[0] == "issue" && args[1] == "view" && args[2] == "42" {
			return `{"number":42,"title":"Current issue","body":"keep working","state":"OPEN","labels":[]}`, nil
		}
		return "", fmt.Errorf("unexpected call: %v", args)
	}

	cfg := config.DefaultConfig()
	state := &core.WakeState{CurrentIssueNumber: 42, CurrentIssueTitle: "Current issue"}
	issue, err := selectWakeIssue(context.Background(), cfg, state)
	if err != nil {
		t.Fatalf("selectWakeIssue() error = %v", err)
	}
	if issue == nil || issue.Number != 42 {
		t.Fatalf("issue = %+v, want #42", issue)
	}
	if len(calls) != 1 || calls[0].args[1] != "view" {
		t.Fatalf("calls = %+v, want single issue view", calls)
	}
}

func TestSelectWakeIssueFallsBackToIssueList(t *testing.T) {
	oldExec := wakeExecCommand
	defer func() { wakeExecCommand = oldExec }()

	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "issue" && args[1] == "list" {
			return `[{"number":7,"title":"First open issue","body":"fix it","labels":[]}]`, nil
		}
		return "", fmt.Errorf("unexpected call: %v", args)
	}

	cfg := config.DefaultConfig()
	issue, err := selectWakeIssue(context.Background(), cfg, &core.WakeState{})
	if err != nil {
		t.Fatalf("selectWakeIssue() error = %v", err)
	}
	if issue == nil || issue.Number != 7 {
		t.Fatalf("issue = %+v, want #7", issue)
	}
}

func TestWakeIssueCloseUsesCommitComment(t *testing.T) {
	oldExec := wakeExecCommand
	defer func() { wakeExecCommand = oldExec }()

	var gotArgs []string
	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "closed", nil
	}

	cfg := config.DefaultConfig()
	if err := wakeIssueClose(context.Background(), cfg, 12, "abc123"); err != nil {
		t.Fatalf("wakeIssueClose() error = %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "issue close 12") || !strings.Contains(joined, "Fixed in abc123") {
		t.Fatalf("args = %q", joined)
	}
}

func TestWakeGitCleanParsesDirtyAndCleanTrees(t *testing.T) {
	oldExec := wakeExecCommand
	defer func() { wakeExecCommand = oldExec }()

	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		return "", nil
	}
	clean, err := wakeGitClean(context.Background())
	if err != nil || !clean {
		t.Fatalf("wakeGitClean() clean = %v err=%v, want true nil", clean, err)
	}

	wakeExecCommand = func(ctx context.Context, stdin string, name string, args ...string) (string, error) {
		return " M cmd/v100/cmd_wake.go\n", nil
	}
	clean, err = wakeGitClean(context.Background())
	if err != nil || clean {
		t.Fatalf("wakeGitClean() clean = %v err=%v, want false nil", clean, err)
	}
}

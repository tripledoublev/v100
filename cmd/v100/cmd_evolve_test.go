package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
)

func TestAvgScore(t *testing.T) {
	results := []evolveRunResult{
		{Value: 1.0},
		{Value: 0.5},
		{Value: 0.0},
	}
	avg := avgScore(results)
	if avg != 0.5 {
		t.Errorf("expected 0.5, got %f", avg)
	}
}

func TestRunMutationTestGatePassesAndExposesCandidatePath(t *testing.T) {
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate_policy.md")
	if err := os.WriteFile(candidatePath, []byte("candidate"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := runMutationTestGate(t.Context(), "evolve-1", "source-1", `test -f "$V100_CANDIDATE_POLICY"`, candidatePath, time.Now().UTC())
	if report.Decision != "passed" {
		t.Fatalf("Decision = %q, want passed: %+v", report.Decision, report)
	}
	if report.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", report.ExitCode)
	}
}

func TestRunMutationTestGateRejectsFailingCommand(t *testing.T) {
	report := runMutationTestGate(t.Context(), "evolve-1", "source-1", `printf nope >&2; exit 7`, "candidate.md", time.Now().UTC())
	if report.Decision != "rejected" {
		t.Fatalf("Decision = %q, want rejected: %+v", report.Decision, report)
	}
	if report.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", report.ExitCode)
	}
	if !strings.Contains(report.RejectedReason, "exit code 7") {
		t.Fatalf("RejectedReason = %q, want exit code", report.RejectedReason)
	}
	if !strings.Contains(report.Stderr, "nope") {
		t.Fatalf("Stderr = %q, want command stderr", report.Stderr)
	}
}

func TestAvgScoreEmpty(t *testing.T) {
	avg := avgScore(nil)
	if avg != 0 {
		t.Errorf("expected 0, got %f", avg)
	}
}

func TestBenchmarkHoldDecisionRejectsRegression(t *testing.T) {
	decision, reason := benchmarkHoldDecision(0.75, 0.50)
	if decision != "rejected" {
		t.Fatalf("decision = %q, want rejected", decision)
	}
	if !strings.Contains(reason, "candidate score 0.50 below baseline 0.75") {
		t.Fatalf("reason = %q, want benchmark regression detail", reason)
	}
}

func TestBenchmarkHoldDecisionAllowsImprovementAndTie(t *testing.T) {
	decision, reason := benchmarkHoldDecision(0.50, 0.75)
	if decision != "recommend_adopt" || reason != "" {
		t.Fatalf("improvement decision = %q/%q, want recommend_adopt with no reason", decision, reason)
	}
	decision, reason = benchmarkHoldDecision(0.50, 0.50)
	if decision != "recommend_reject" || reason != "" {
		t.Fatalf("tie decision = %q/%q, want recommend_reject with no reason", decision, reason)
	}
}

func TestEvolveAdoptRejectsBenchmarkHoldRejectedReport(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		evolveID := "evolve-rejected"
		evolveDir := filepath.Join("runs", evolveID)
		if err := os.MkdirAll(evolveDir, 0o755); err != nil {
			return err
		}
		candidatePath := filepath.Join(evolveDir, "candidate_policy.md")
		if err := os.WriteFile(candidatePath, []byte("candidate"), 0o644); err != nil {
			return err
		}
		report := evolutionReport{
			EvolveID:       evolveID,
			Decision:       "rejected",
			CandidatePath:  candidatePath,
			RejectedReason: "benchmark hold failed: candidate score 0.10 below baseline 1.00",
		}
		data, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(evolveDir, "evolution.json"), data, 0o644); err != nil {
			return err
		}
		cfgPath := ""
		cmd := evolveAdoptCmd(&cfgPath)
		err = cmd.RunE(cmd, []string{evolveID})
		if err == nil {
			t.Fatal("expected rejected evolution report to block adoption")
		}
		if !strings.Contains(err.Error(), "refusing to adopt rejected candidate") {
			t.Fatalf("unexpected error: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyEvolutionWithRollbackRestoresPolicyOnTestFailure(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		cfg := config.DefaultConfig()
		targetPath := resolveDefaultPolicyPath(cfg)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		original := []byte("original policy")
		if err := os.WriteFile(targetPath, original, 0o644); err != nil {
			return err
		}
		evolveDir := filepath.Join("runs", "evolve-rollback")
		if err := os.MkdirAll(evolveDir, 0o755); err != nil {
			return err
		}
		candidatePath := filepath.Join(evolveDir, "candidate_policy.md")
		if err := os.WriteFile(candidatePath, []byte("candidate policy"), 0o644); err != nil {
			return err
		}
		report, err := applyEvolutionWithRollback(cfg, "evolve-rollback", candidatePath, `exit 9`)
		if err == nil {
			return os.ErrInvalid
		}
		if !strings.Contains(err.Error(), "test gate failed") {
			return err
		}
		got, err := os.ReadFile(targetPath)
		if err != nil {
			return err
		}
		if string(got) != string(original) {
			return os.ErrInvalid
		}
		if !report.RolledBack {
			return os.ErrInvalid
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunEvolutionAdoptTestUsesCandidatePathEnv(t *testing.T) {
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate_policy.md")
	if err := os.WriteFile(candidatePath, []byte("candidate"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := runEvolutionAdoptTest(context.Background(), `test "$V100_CANDIDATE_POLICY" = "`+candidatePath+`"`, candidatePath)
	if report.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0: %+v", report.ExitCode, report)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Errorf("expected 'hello...', got %q", truncate("hello world", 5))
	}
}

func TestResolveModelFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	model := resolveModel(cfg, "nonexistent")
	if model != "" {
		t.Errorf("expected empty model for unknown provider, got %q", model)
	}
}

func TestEvolveCommandRegistered(t *testing.T) {
	root := rootCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "evolve" {
			// Check subcommands
			var found []string
			for _, sub := range cmd.Commands() {
				found = append(found, sub.Name())
			}
			if len(found) < 2 {
				t.Errorf("expected at least 2 subcommands (once, adopt), got %v", found)
			}
			return
		}
	}
	t.Error("evolve command not registered")
}

func TestResolveBenchProviderModelUsesFallbacks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers["gemini"] = config.ProviderConfig{Type: "gemini", DefaultModel: "gemini-default"}

	providerName, model := resolveBenchProviderModel(cfg, core.BenchVariant{}, "gemini")
	if providerName != "gemini" {
		t.Fatalf("providerName = %q, want gemini", providerName)
	}
	if model != "gemini-default" {
		t.Fatalf("model = %q, want gemini-default", model)
	}

	providerName, model = resolveBenchProviderModel(cfg, core.BenchVariant{Provider: "codex", Model: "gpt-5.4"}, "gemini")
	if providerName != "codex" || model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.4", providerName, model)
	}
}

func TestNewEvolveBenchMetaSetsParentRunID(t *testing.T) {
	meta := newEvolveBenchMeta("child-run", "parent-run", "ux", "candidate", "fast", "codex", "gpt-5.4", 1)
	if meta.RunID != "child-run" {
		t.Fatalf("RunID = %q, want child-run", meta.RunID)
	}
	if meta.ParentRunID != "parent-run" {
		t.Fatalf("ParentRunID = %q, want parent-run", meta.ParentRunID)
	}
	if meta.Provider != "codex" || meta.Model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.4", meta.Provider, meta.Model)
	}
	if got := meta.Tags["policy_variant"]; got != "candidate" {
		t.Fatalf("policy_variant = %q, want candidate", got)
	}
	if got := meta.Tags["variant"]; got != "fast" {
		t.Fatalf("variant = %q, want fast", got)
	}
	if got := meta.Tags["prompt_id"]; got != "2" {
		t.Fatalf("prompt_id = %q, want 2", got)
	}
}

func TestNewMutationRejectionReport(t *testing.T) {
	created := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	mutation := eval.PolicyMutationResult{
		OriginalPolicy:  "short",
		CandidatePolicy: "a much longer candidate",
		Rationale:       "too broad",
		RejectedReason:  "mutated policy exceeds max growth: +18 > +10",
	}
	report := newMutationRejectionReport("evolve-1", "source-1", mutation, eval.MutationBudgets{MaxPromptGrowthChars: 10}, "runs/evolve-1/candidate_policy.rejected.md", created)

	if report.Decision != "rejected" {
		t.Fatalf("Decision = %q, want rejected", report.Decision)
	}
	if report.RejectedReason != mutation.RejectedReason {
		t.Fatalf("RejectedReason = %q, want %q", report.RejectedReason, mutation.RejectedReason)
	}
	if report.OriginalChars != len(mutation.OriginalPolicy) || report.CandidateChars != len(mutation.CandidatePolicy) {
		t.Fatalf("chars = %d/%d, want %d/%d", report.OriginalChars, report.CandidateChars, len(mutation.OriginalPolicy), len(mutation.CandidatePolicy))
	}
	if report.MaxPromptChars != eval.DefaultMutationBudgets().MaxPromptChars {
		t.Fatalf("MaxPromptChars = %d, want default", report.MaxPromptChars)
	}
	if report.MaxPromptGrowth != 10 {
		t.Fatalf("MaxPromptGrowth = %d, want 10", report.MaxPromptGrowth)
	}
	if !report.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want %v", report.CreatedAt, created)
	}
}

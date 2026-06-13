package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/eval"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/ui"
)

// evolveRunResult holds the outcome of a single bench prompt run.
type evolveRunResult struct {
	RunID    string  `json:"run_id"`
	PromptID int     `json:"prompt_id"`
	Score    string  `json:"score"`
	Value    float64 `json:"value"`
	Notes    string  `json:"notes,omitempty"`
}

// evolutionReport is the full artifact written to runs/<evolve_id>/evolution.json.
type evolutionReport struct {
	EvolveID         string            `json:"evolve_id"`
	SourceTraceID    string            `json:"source_trace_id"`
	BenchPath        string            `json:"bench_path"`
	BaselineResults  []evolveRunResult `json:"baseline_results"`
	CandidateResults []evolveRunResult `json:"candidate_results"`
	BaselineScore    float64           `json:"baseline_score"`
	CandidateScore   float64           `json:"candidate_score"`
	Decision         string            `json:"decision"`
	Rationale        string            `json:"rationale"`
	CandidatePath    string            `json:"candidate_path"`
	RejectedReason   string            `json:"rejected_reason,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
}

// mutationRejectionReport records why a generated mutation was rejected before
// benchmark execution.
type mutationRejectionReport struct {
	EvolveID            string    `json:"evolve_id"`
	SourceTraceID       string    `json:"source_trace_id"`
	Decision            string    `json:"decision"`
	RejectedReason      string    `json:"rejected_reason"`
	Rationale           string    `json:"rationale"`
	OriginalChars       int       `json:"original_chars"`
	CandidateChars      int       `json:"candidate_chars"`
	MaxPromptChars      int       `json:"max_prompt_chars"`
	MaxPromptGrowth     int       `json:"max_prompt_growth_chars"`
	CandidatePolicyPath string    `json:"candidate_policy_path"`
	CreatedAt           time.Time `json:"created_at"`
}

type mutationTestGateReport struct {
	EvolveID            string    `json:"evolve_id"`
	SourceTraceID       string    `json:"source_trace_id"`
	Decision            string    `json:"decision"`
	TestCommand         string    `json:"test_command"`
	ExitCode            int       `json:"exit_code"`
	Stdout              string    `json:"stdout,omitempty"`
	Stderr              string    `json:"stderr,omitempty"`
	RejectedReason      string    `json:"rejected_reason,omitempty"`
	CandidatePolicyPath string    `json:"candidate_policy_path"`
	CreatedAt           time.Time `json:"created_at"`
}

type evolutionApplyReport struct {
	TargetPath     string
	BackupPath     string
	CandidatePath  string
	AppliedAt      time.Time
	RolledBack     bool
	RollbackReason string
	TestCommand    string
	TestExitCode   int
	TestOutput     string
}

func newMutationRejectionReport(evolveID, traceID string, mutation eval.PolicyMutationResult, budgets eval.MutationBudgets, candidatePath string, createdAt time.Time) mutationRejectionReport {
	budgets = budgets.NormalizedForReport()
	return mutationRejectionReport{
		EvolveID:            evolveID,
		SourceTraceID:       traceID,
		Decision:            "rejected",
		RejectedReason:      mutation.RejectedReason,
		Rationale:           mutation.Rationale,
		OriginalChars:       len(mutation.OriginalPolicy),
		CandidateChars:      len(mutation.CandidatePolicy),
		MaxPromptChars:      budgets.MaxPromptChars,
		MaxPromptGrowth:     budgets.MaxPromptGrowthChars,
		CandidatePolicyPath: candidatePath,
		CreatedAt:           createdAt,
	}
}

func resolveBenchProviderModel(cfg *config.Config, variant core.BenchVariant, fallbackProvider string) (string, string) {
	providerName := strings.TrimSpace(variant.Provider)
	if providerName == "" {
		providerName = strings.TrimSpace(fallbackProvider)
	}
	model := strings.TrimSpace(variant.Model)
	if model == "" {
		model = resolveModel(cfg, providerName)
	}
	return providerName, model
}

func newEvolveBenchMeta(runID, parentRunID, benchName, policyVariant, variantName, providerName, model string, promptIndex int) core.RunMeta {
	if strings.TrimSpace(variantName) == "" {
		variantName = "default"
	}
	return core.RunMeta{
		RunID:           runID,
		Name:            fmt.Sprintf("evolve:%s/%s/%s/%d", benchName, policyVariant, variantName, promptIndex+1),
		Tags:            map[string]string{"type": "evolve.bench", "policy_variant": policyVariant, "variant": variantName, "prompt_id": fmt.Sprintf("%d", promptIndex+1)},
		Provider:        providerName,
		Model:           model,
		SourceWorkspace: ".",
		ParentRunID:     parentRunID,
		CreatedAt:       time.Now().UTC(),
	}
}

func evolveCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolve",
		Short: "Evolve agent policy through benchmarked mutation",
	}
	cmd.AddCommand(evolveOnceCmd(cfgPath))
	cmd.AddCommand(evolveAdoptCmd(cfgPath))
	return cmd
}

func evolveOnceCmd(cfgPath *string) *cobra.Command {
	var (
		benchPath    string
		providerName string
		evalProvider string
		traceID      string
		evalType     string
		rubric       string
		testCommand  string
		budgets      eval.MutationBudgets
	)

	cmd := &cobra.Command{
		Use:   "once",
		Short: "Run a single evolution cycle: mutate policy, benchmark, compare",
		RunE: func(cmd *cobra.Command, args []string) error {
			if benchPath == "" {
				return fmt.Errorf("--bench is required")
			}
			if traceID == "" {
				return fmt.Errorf("--trace is required")
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			ctx := context.Background()

			// Resolve provider for mutation + bench execution
			if providerName == "" {
				providerName = cfg.Defaults.Provider
			}
			prov, err := buildProvider(cfg, providerName)
			if err != nil {
				return fmt.Errorf("build provider: %w", err)
			}
			model := resolveModel(cfg, providerName)

			// Resolve eval provider (separate judge)
			var evalProv providers.Provider
			var evalModel string
			if evalProvider != "" && evalProvider != providerName {
				evalProv, err = buildProvider(cfg, evalProvider)
				if err != nil {
					return fmt.Errorf("build eval provider: %w", err)
				}
				evalModel = resolveModel(cfg, evalProvider)
			} else {
				evalProv = prov
				evalModel = model
			}

			// Load source trace
			sourceDir, err := findRunDir(traceID)
			if err != nil {
				return fmt.Errorf("find source run: %w", err)
			}
			tracePath := filepath.Join(sourceDir, "trace.jsonl")
			events, err := core.ReadAll(tracePath)
			if err != nil {
				return fmt.Errorf("read source trace: %w", err)
			}

			// Load current policy
			pol := loadPolicy(cfg, "default")
			fmt.Printf("%s  Loaded current policy (%d chars)\n", ui.Info("evolve"), len(pol.SystemPrompt))

			// Mutate policy
			fmt.Printf("%s  Mutating policy based on trace %s...\n", ui.Info("evolve"), traceID)
			mutation, err := eval.MutatePolicy(ctx, prov, model, budgets, pol.SystemPrompt, events)
			if err != nil {
				return fmt.Errorf("mutate policy: %w", err)
			}

			if mutation.RejectedReason != "" {
				evolveID := newRunID()
				evolveDir := filepath.Join("runs", evolveID)
				if err := os.MkdirAll(evolveDir, 0o755); err != nil {
					return err
				}
				candidatePath := filepath.Join(evolveDir, "candidate_policy.rejected.md")
				if err := os.WriteFile(candidatePath, []byte(mutation.CandidatePolicy), 0o644); err != nil {
					return err
				}
				now := time.Now().UTC()
				report := newMutationRejectionReport(evolveID, traceID, mutation, budgets, candidatePath, now)
				reportBytes, _ := json.MarshalIndent(report, "", "  ")
				if err := os.WriteFile(filepath.Join(evolveDir, "mutation_rejection.json"), reportBytes, 0o644); err != nil {
					return err
				}
				meta := core.RunMeta{
					RunID:           evolveID,
					Name:            "evolve:rejected",
					Tags:            map[string]string{"type": "evolve", "source_trace": traceID, "decision": "rejected"},
					Provider:        prov.Name(),
					Model:           model,
					ModelMetadata:   resolveProviderMetadata(ctx, prov, model, providers.ModelMetadata{}),
					SourceWorkspace: resolveWorkspace("", evolveDir),
					CreatedAt:       now,
				}
				if err := core.WriteMeta(evolveDir, meta); err != nil {
					return err
				}
				trace, err := core.OpenTrace(filepath.Join(evolveDir, "trace.jsonl"))
				if err != nil {
					return err
				}
				if err := appendTraceEvent(trace, evolveID, core.EventPolicyEvolve, core.PolicyEvolvePayload{
					EvolveID:       evolveID,
					Decision:       "rejected",
					Rationale:      mutation.Rationale,
					CandidatePath:  candidatePath,
					SourceTraceID:  traceID,
					RejectedReason: mutation.RejectedReason,
				}); err != nil {
					_ = trace.Close()
					return err
				}
				if err := appendTraceEvent(trace, evolveID, core.EventRunEnd, core.RunEndPayload{Reason: "mutation_rejected"}); err != nil {
					_ = trace.Close()
					return err
				}
				if err := trace.Close(); err != nil {
					return err
				}
				fmt.Printf("%s  Mutation rejected: %s\n", ui.Warn("reject"), mutation.RejectedReason)
				fmt.Printf("%s  Candidate size: %d chars (current %d)\n", ui.Info("size"), len(mutation.CandidatePolicy), len(mutation.OriginalPolicy))
				fmt.Printf("%s  Rejection report: %s\n", ui.Info("report"), filepath.Join(evolveDir, "mutation_rejection.json"))
				return nil
			}
			if mutation.MutatedPolicy == mutation.OriginalPolicy {
				fmt.Printf("%s  No issues detected — policy unchanged\n", ui.OK("done"))
				return nil
			}

			fmt.Printf("%s  Mutation rationale: %s\n", ui.OK("mutated"), truncate(mutation.Rationale, 200))

			// Create evolve run directory
			evolveID := newRunID()
			evolveDir := filepath.Join("runs", evolveID)
			if err := os.MkdirAll(evolveDir, 0o755); err != nil {
				return err
			}

			// Load bench config
			bc, err := core.LoadBenchConfig(benchPath)
			if err != nil {
				return fmt.Errorf("load bench config: %w", err)
			}

			evolveTrace, err := core.OpenTrace(filepath.Join(evolveDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			defer func() { _ = evolveTrace.Close() }()
			evolveWorkspace := resolveWorkspace("", evolveDir)
			evolveMetadata := resolveProviderMetadata(ctx, prov, model, providers.ModelMetadata{})
			evolveMeta := core.RunMeta{
				RunID:           evolveID,
				Name:            "evolve:" + bc.Name,
				Tags:            map[string]string{"type": "evolve", "source_trace": traceID},
				Provider:        prov.Name(),
				Model:           model,
				ModelMetadata:   evolveMetadata,
				SourceWorkspace: evolveWorkspace,
				CreatedAt:       time.Now().UTC(),
			}
			if err := core.WriteMeta(evolveDir, evolveMeta); err != nil {
				return err
			}
			if err := appendTraceEvent(evolveTrace, evolveID, core.EventRunStart, core.RunStartPayload{
				Policy:        pol.Name,
				Provider:      prov.Name(),
				Model:         model,
				Workspace:     evolveWorkspace,
				ModelMetadata: evolveMetadata,
			}); err != nil {
				return err
			}
			reason := "completed"
			defer func() {
				_ = appendTraceEvent(evolveTrace, evolveID, core.EventRunEnd, core.RunEndPayload{Reason: reason})
			}()

			// Write candidate policy
			candidatePath := filepath.Join(evolveDir, "candidate_policy.md")
			if err := os.WriteFile(candidatePath, []byte(mutation.MutatedPolicy), 0o644); err != nil {
				reason = "error"
				return err
			}
			fmt.Printf("%s  Candidate policy written to %s\n", ui.OK("saved"), candidatePath)

			if strings.TrimSpace(testCommand) != "" {
				testReport := runMutationTestGate(ctx, evolveID, traceID, testCommand, candidatePath, time.Now().UTC())
				reportBytes, _ := json.MarshalIndent(testReport, "", "  ")
				_ = os.WriteFile(filepath.Join(evolveDir, "mutation_test_gate.json"), reportBytes, 0o644)
				if testReport.Decision == "rejected" {
					reason = "mutation_test_gate_failed"
					if evolveMeta.Tags == nil {
						evolveMeta.Tags = map[string]string{}
					}
					evolveMeta.Tags["decision"] = "rejected"
					evolveMeta.Tags["rejected_reason"] = testReport.RejectedReason
					_ = core.WriteMeta(evolveDir, evolveMeta)
					if err := appendTraceEvent(evolveTrace, evolveID, core.EventPolicyEvolve, core.PolicyEvolvePayload{
						EvolveID:       evolveID,
						Decision:       "rejected",
						Rationale:      mutation.Rationale,
						CandidatePath:  candidatePath,
						SourceTraceID:  traceID,
						RejectedReason: testReport.RejectedReason,
					}); err != nil {
						return err
					}
					fmt.Printf("%s  Mutation rejected by test gate: %s\n", ui.Warn("reject"), testReport.RejectedReason)
					fmt.Printf("%s  Test report: %s\n", ui.Info("report"), filepath.Join(evolveDir, "mutation_test_gate.json"))
					return nil
				}
				fmt.Printf("%s  Test gate passed: %s\n", ui.OK("test"), testCommand)
			}

			if evalType == "" {
				evalType = "reflective"
			}

			// Run baseline
			fmt.Printf("\n%s\n", ui.Header("Baseline (current policy)"))
			baselineResults, err := runBenchWithPolicy(ctx, cfg, bc, pol.SystemPrompt, providerName, evalProv, evalModel, evalType, rubric, evolveID, "baseline")
			if err != nil {
				reason = "error"
				return fmt.Errorf("baseline bench: %w", err)
			}

			// Run candidate
			fmt.Printf("\n%s\n", ui.Header("Candidate (mutated policy)"))
			candidateResults, err := runBenchWithPolicy(ctx, cfg, bc, mutation.MutatedPolicy, providerName, evalProv, evalModel, evalType, rubric, evolveID, "candidate")
			if err != nil {
				reason = "error"
				return fmt.Errorf("candidate bench: %w", err)
			}

			// Compare
			baselineAvg := avgScore(baselineResults)
			candidateAvg := avgScore(candidateResults)
			decision, rejectedReason := benchmarkHoldDecision(baselineAvg, candidateAvg)

			// Print comparison
			fmt.Printf("\n%s\n", ui.Header("Evolution Results"))
			fmt.Printf("  Baseline score:  %.2f (%d runs)\n", baselineAvg, len(baselineResults))
			fmt.Printf("  Candidate score: %.2f (%d runs)\n", candidateAvg, len(candidateResults))
			fmt.Printf("  Delta:           %+.2f\n", candidateAvg-baselineAvg)
			switch decision {
			case "recommend_adopt":
				fmt.Printf("  %s Recommend adoption\n", ui.OK("verdict"))
			case "rejected":
				fmt.Printf("  %s Rejected by benchmark hold: %s\n", ui.Fail("verdict"), rejectedReason)
			default:
				fmt.Printf("  %s Recommend rejection\n", ui.Fail("verdict"))
			}
			fmt.Printf("\n  Candidate: %s\n", candidatePath)
			fmt.Printf("  Adopt:     v100 evolve adopt %s\n", evolveID)

			// Write evolution report
			report := evolutionReport{
				EvolveID:         evolveID,
				SourceTraceID:    traceID,
				BenchPath:        benchPath,
				BaselineResults:  baselineResults,
				CandidateResults: candidateResults,
				BaselineScore:    baselineAvg,
				CandidateScore:   candidateAvg,
				Decision:         decision,
				Rationale:        mutation.Rationale,
				CandidatePath:    candidatePath,
				RejectedReason:   rejectedReason,
				CreatedAt:        time.Now().UTC(),
			}
			reportBytes, _ := json.MarshalIndent(report, "", "  ")
			_ = os.WriteFile(filepath.Join(evolveDir, "evolution.json"), reportBytes, 0o644)

			if evolveMeta.Tags == nil {
				evolveMeta.Tags = map[string]string{}
			}
			evolveMeta.Tags["decision"] = decision
			if rejectedReason != "" {
				evolveMeta.Tags["rejected_reason"] = rejectedReason
			}
			if err := core.WriteMeta(evolveDir, evolveMeta); err != nil {
				return err
			}
			if err := appendTraceEvent(evolveTrace, evolveID, core.EventPolicyEvolve, core.PolicyEvolvePayload{
				EvolveID:       evolveID,
				BaselineScore:  baselineAvg,
				CandidateScore: candidateAvg,
				Decision:       decision,
				Rationale:      mutation.Rationale,
				CandidatePath:  candidatePath,
				SourceTraceID:  traceID,
				RejectedReason: rejectedReason,
			}); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&benchPath, "bench", "", "path to bench.toml file (required)")
	cmd.Flags().StringVar(&providerName, "provider", "", "provider for mutation + bench runs")
	cmd.Flags().StringVar(&evalProvider, "eval-provider", "", "separate provider for scoring (avoids self-confirmation)")
	cmd.Flags().StringVar(&traceID, "trace", "", "source run ID whose failures inform mutation (required)")
	cmd.Flags().StringVar(&evalType, "eval", "reflective", "scorer type")
	cmd.Flags().StringVar(&rubric, "rubric", "", "override rubric for all prompts")
	cmd.Flags().StringVar(&testCommand, "test-command", "", "shell command that must pass before evaluating or accepting a mutated policy")
	bindMutationBudgetFlags(cmd, &budgets)
	return cmd
}

func benchmarkHoldDecision(baselineAvg, candidateAvg float64) (decision, rejectedReason string) {
	if candidateAvg > baselineAvg {
		return "recommend_adopt", ""
	}
	if candidateAvg < baselineAvg {
		return "rejected", fmt.Sprintf("benchmark hold failed: candidate score %.2f below baseline %.2f", candidateAvg, baselineAvg)
	}
	return "recommend_reject", ""
}

func runMutationTestGate(ctx context.Context, evolveID, traceID, command, candidatePath string, createdAt time.Time) mutationTestGateReport {
	report := mutationTestGateReport{
		EvolveID:            evolveID,
		SourceTraceID:       traceID,
		Decision:            "passed",
		TestCommand:         command,
		CandidatePolicyPath: candidatePath,
		CreatedAt:           createdAt,
	}
	if strings.TrimSpace(command) == "" {
		return report
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), "V100_CANDIDATE_POLICY="+candidatePath)
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	report.Stdout = stdout.String()
	report.Stderr = stderr.String()
	if err == nil {
		return report
	}
	report.Decision = "rejected"
	report.ExitCode = -1
	report.RejectedReason = err.Error()
	if exitErr, ok := err.(*exec.ExitError); ok {
		report.ExitCode = exitErr.ExitCode()
		report.RejectedReason = fmt.Sprintf("test command failed with exit code %d", report.ExitCode)
	}
	if ctx.Err() != nil {
		report.RejectedReason = "test command timed out"
	}
	return report
}

// runBenchWithPolicy executes all prompts in a bench config under a given system policy,
// scores each run, and returns results.
func runBenchWithPolicy(
	ctx context.Context,
	cfg *config.Config,
	bc *core.BenchConfig,
	systemPrompt string,
	providerName string,
	evalProv providers.Provider,
	evalModel string,
	evalType string,
	rubricOverride string,
	parentRunID string,
	policyVariant string,
) ([]evolveRunResult, error) {
	var results []evolveRunResult

	// Use the first variant, or a default if none defined
	variant := core.BenchVariant{Name: "default", Provider: providerName}
	if len(bc.Variants) > 0 {
		variant = bc.Variants[0]
	}
	runProvider, runModel := resolveBenchProviderModel(cfg, variant, providerName)
	variant.Provider = runProvider
	variant.Model = runModel

	genParams := providers.GenParams{
		Temperature: variant.Temperature,
		TopP:        variant.TopP,
		TopK:        variant.TopK,
		MaxTokens:   variant.MaxTokens,
		Seed:        variant.Seed,
	}

	for pi, prompt := range bc.Prompts {
		fmt.Printf("  %s  prompt %d/%d\n", ui.Info("run"), pi+1, len(bc.Prompts))

		runID := newRunID()
		runDir := filepath.Join("runs", runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return nil, err
		}
		meta := newEvolveBenchMeta(runID, parentRunID, bc.Name, policyVariant, variant.Name, runProvider, runModel, pi)
		if err := core.WriteMeta(runDir, meta); err != nil {
			return nil, err
		}

		tracePath := filepath.Join(runDir, "trace.jsonl")
		trace, err := core.OpenTrace(tracePath)
		if err != nil {
			return nil, err
		}

		coreRun := &core.Run{ID: runID, Dir: runDir, TraceFile: tracePath}

		var sSession executor.Session
		var sMapper *core.PathMapper
		var sWorkspace string
		sSession, sMapper, sWorkspace, err = buildSandboxSession(cfg, runID, ".", "runs")
		if err != nil {
			_ = trace.Close()
			return nil, err
		}
		if cfg.Sandbox.Enabled {
			defer func() { _ = sSession.Close() }()
		}

		reg := buildToolRegistry(cfg)
		if err := validateToolRegistry(reg); err != nil {
			_ = trace.Close()
			return nil, err
		}

		// Build policy with overridden system prompt
		pol := loadPolicy(cfg, "default")
		pol.SystemPrompt = systemPrompt

		solver, err := buildSolver(cfg, variant.Solver)
		if err != nil {
			_ = trace.Close()
			return nil, err
		}

		prov, err := buildProviderWithModel(cfg, variant.Provider, runModel)
		if err != nil {
			_ = trace.Close()
			return nil, err
		}

		budgetSteps := variant.BudgetSteps
		if budgetSteps == 0 {
			budgetSteps = cfg.Defaults.BudgetSteps
		}
		budget := core.NewBudgetTracker(&core.Budget{
			MaxSteps:   budgetSteps,
			MaxTokens:  cfg.Defaults.BudgetTokens,
			MaxCostUSD: cfg.Defaults.BudgetCostUSD,
		})

		renderer := ui.NewCLIRenderer()
		confirmFn := func(_, _ string) bool { return true }
		outputFn := core.OutputFn(renderer.RenderEvent)
		toolEnv, redactToolOutput := buildToolRuntime(cfg)
		registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, sWorkspace, pol.MaxToolCallsPerStep, sSession, sMapper, toolEnv, redactToolOutput)

		loop := &core.Loop{
			Run:              coreRun,
			Provider:         prov,
			Tools:            reg,
			Policy:           pol,
			Trace:            trace,
			Budget:           budget,
			ConfirmFn:        confirmFn,
			OutputFn:         outputFn,
			GenParams:        genParams,
			Solver:           solver,
			Session:          sSession,
			Mapper:           sMapper,
			ToolEnv:          append([]string(nil), toolEnv...),
			RedactToolOutput: redactToolOutput,
			NetworkTier:      loopNetworkTier(cfg),
			Snapshots:        buildSnapshotManager(cfg, sWorkspace),
		}

		loop.Hooks = append(loop.Hooks, core.ThresholdHook(5))
		loop.Hooks = append(loop.Hooks, core.DeduplicationHook(2))

		metadata := resolveProviderMetadata(ctx, prov, runModel, providers.ModelMetadata{})
		loop.ModelMetadata = metadata
		persistRunSelection(runDir, prov.Name(), runModel, metadata, false)

		_ = loop.EmitRunStart(core.RunStartPayload{
			Policy:        pol.Name,
			Provider:      prov.Name(),
			Model:         runModel,
			Workspace:     traceWorkspace(cfg, sWorkspace),
			ModelMetadata: metadata,
		})

		reason := "completed"
		if err := loop.Step(ctx, prompt.Message); err != nil {
			reason = "error"
		}
		_ = loop.EmitRunEnd(reason, "")
		_, _ = finalizeSandboxRun(cfg, coreRun, reason, sMapper)
		_ = trace.Close()

		// Score the run
		activeEvalType := evalType
		if activeEvalType == "" {
			activeEvalType = prompt.Scorer
		}

		result := evolveRunResult{RunID: runID, PromptID: pi}

		if activeEvalType != "" {
			scorer, err := eval.LookupScorer(activeEvalType, evalProv, evalModel)
			if err == nil {
				evts, err := core.ReadAll(tracePath)
				if err == nil {
					r := prompt.Expected
					if rubricOverride != "" {
						r = rubricOverride
					}
					res, err := scorer.Score(ctx, evts, r)
					if err == nil {
						result.Score = res.Score
						result.Value = res.Value
						result.Notes = res.Notes

						meta, _ := core.ReadMeta(runDir)
						meta.Score = res.Score
						meta.ScoreNotes = res.Notes
						_ = core.WriteMeta(runDir, meta)
					}
				}
			}
		}

		verdict := strings.ToUpper(result.Score)
		if verdict == "" {
			verdict = "UNSCORED"
		}
		fmt.Printf("    %s  %s (%.2f)\n", ui.OK("scored"), verdict, result.Value)
		results = append(results, result)
	}

	return results, nil
}

func avgScore(results []evolveRunResult) float64 {
	if len(results) == 0 {
		return 0
	}
	var sum float64
	for _, r := range results {
		sum += r.Value
	}
	return sum / float64(len(results))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func evolveAdoptCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "adopt <evolve_id>",
		Short: "Adopt a candidate policy from a completed evolution cycle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			evolveID := args[0]
			evolveDir, err := findRunDir(evolveID)
			if err != nil {
				return fmt.Errorf("find evolve run: %w", err)
			}

			// Read evolution report
			reportPath := filepath.Join(evolveDir, "evolution.json")
			reportBytes, err := os.ReadFile(reportPath)
			if err != nil {
				return fmt.Errorf("read evolution report: %w", err)
			}
			var report evolutionReport
			if err := json.Unmarshal(reportBytes, &report); err != nil {
				return fmt.Errorf("parse evolution report: %w", err)
			}
			if report.Decision == "rejected" {
				reason := strings.TrimSpace(report.RejectedReason)
				if reason == "" {
					reason = "evolution report decision is rejected"
				}
				return fmt.Errorf("refusing to adopt rejected candidate: %s", reason)
			}

			// Determine target path
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			applyReport, err := applyEvolutionWithRollback(cfg, evolveID, report.CandidatePath, "")
			if err != nil {
				return err
			}
			candidateBytes, err := os.ReadFile(report.CandidatePath)
			if err != nil {
				return fmt.Errorf("read candidate policy: %w", err)
			}

			// Show diff summary
			fmt.Printf("%s\n", ui.Header("Policy Adoption"))
			fmt.Printf("  Evolve ID:       %s\n", evolveID)
			fmt.Printf("  Baseline score:  %.2f\n", report.BaselineScore)
			fmt.Printf("  Candidate score: %.2f\n", report.CandidateScore)
			fmt.Printf("  Decision:        %s\n", report.Decision)
			fmt.Printf("  Target:          %s\n", applyReport.TargetPath)
			fmt.Printf("  Candidate size:  %d chars\n\n", len(candidateBytes))

			fmt.Printf("%s  Policy adopted at %s\n", ui.OK("done"), applyReport.TargetPath)
			return nil
		},
	}
}

func applyEvolutionWithRollback(cfg *config.Config, evolveID, candidatePath, testCommand string) (evolutionApplyReport, error) {
	targetPath := resolveDefaultPolicyPath(cfg)
	candidateBytes, err := os.ReadFile(candidatePath)
	if err != nil {
		return evolutionApplyReport{}, fmt.Errorf("read candidate policy: %w", err)
	}
	if strings.TrimSpace(string(candidateBytes)) == "" {
		return evolutionApplyReport{}, fmt.Errorf("candidate policy is empty")
	}

	report := evolutionApplyReport{
		TargetPath:    targetPath,
		CandidatePath: candidatePath,
		AppliedAt:     time.Now().UTC(),
		TestCommand:   strings.TrimSpace(testCommand),
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return report, err
	}

	backupPath := targetPath + ".evolve-backup"
	if current, err := os.ReadFile(targetPath); err == nil && len(current) > 0 {
		if err := os.WriteFile(backupPath, current, 0o644); err != nil {
			return report, fmt.Errorf("write policy backup: %w", err)
		}
		report.BackupPath = backupPath
	}

	if err := os.WriteFile(targetPath, candidateBytes, 0o644); err != nil {
		if report.BackupPath != "" {
			_ = restorePolicyBackup(targetPath, report.BackupPath)
		}
		return report, fmt.Errorf("write policy: %w", err)
	}

	if report.TestCommand != "" {
		testReport := runEvolutionAdoptTest(context.Background(), testCommand, candidatePath)
		report.TestExitCode = testReport.ExitCode
		report.TestOutput = strings.TrimSpace(testReport.Stdout + "\n" + testReport.Stderr)
		if testReport.ExitCode != 0 {
			report.RolledBack = true
			report.RollbackReason = testReport.RejectedReason
			if rollbackErr := restorePolicyBackup(targetPath, report.BackupPath); rollbackErr != nil {
				return report, fmt.Errorf("test gate failed: %s; rollback failed: %w", testReport.RejectedReason, rollbackErr)
			}
			return report, fmt.Errorf("test gate failed: %s", testReport.RejectedReason)
		}
	}

	if report.BackupPath != "" {
		_ = os.Remove(report.BackupPath)
	}
	return report, nil
}

func restorePolicyBackup(targetPath, backupPath string) error {
	if backupPath == "" {
		return os.Remove(targetPath)
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return err
	}
	return os.Remove(backupPath)
}

type evolutionAdoptTestReport struct {
	ExitCode       int
	Stdout         string
	Stderr         string
	RejectedReason string
}

func runEvolutionAdoptTest(ctx context.Context, testCommand, candidatePath string) evolutionAdoptTestReport {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", testCommand)
	cmd.Env = append(os.Environ(),
		"V100_CANDIDATE_POLICY="+candidatePath,
	)
	out, err := cmd.CombinedOutput()
	report := evolutionAdoptTestReport{Stdout: string(out)}
	if err == nil {
		return report
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		report.ExitCode = exitErr.ExitCode()
		report.Stderr = string(out)
		report.RejectedReason = fmt.Sprintf("test command failed with exit code %d", report.ExitCode)
		return report
	}
	report.ExitCode = 1
	report.Stderr = string(out)
	report.RejectedReason = err.Error()
	return report
}

// resolveDefaultPolicyPath returns the path where the default policy file lives.
func resolveDefaultPolicyPath(cfg *config.Config) string {
	if pc, ok := cfg.Policies["default"]; ok && pc.SystemPromptPath != "" {
		path := pc.SystemPromptPath
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		}
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "v100", "policies", "default.md")
}

// resolveModel returns the default model for a given provider from config.
func resolveModel(cfg *config.Config, providerName string) string {
	if pc, ok := cfg.Providers[providerName]; ok && pc.DefaultModel != "" {
		return pc.DefaultModel
	}
	return ""
}

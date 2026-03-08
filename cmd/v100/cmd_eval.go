package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/eval"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/ui"
)

func scoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "score <run_id> <pass|fail|partial> [notes...]",
		Short: "Score a completed run",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			score := args[1]
			if score != "pass" && score != "fail" && score != "partial" {
				return fmt.Errorf("score must be pass, fail, or partial")
			}
			notes := ""
			if len(args) > 2 {
				notes = strings.Join(args[2:], " ")
			}

			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			meta, err := core.ReadMeta(runDir)
			if err != nil {
				// Create minimal meta for old runs
				meta = core.RunMeta{RunID: runID, CreatedAt: time.Now().UTC()}
			}
			meta.Score = score
			meta.ScoreNotes = notes
			if err := core.WriteMeta(runDir, meta); err != nil {
				return err
			}
			fmt.Println(ui.OK(fmt.Sprintf("Scored run %s: %s", runID, score)))
			return nil
		},
	}
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats <run_id>",
		Short: "Show statistics for a completed run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir, err := findRunDir(args[0])
			if err != nil {
				return err
			}
			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			stats := core.ComputeStats(events)
			// Enrich with meta score if available
			if meta, err := core.ReadMeta(runDir); err == nil {
				stats.Score = meta.Score
				if stats.ModelMetadata == (providers.ModelMetadata{}) {
					stats.ModelMetadata = meta.ModelMetadata
				}
			}
			fmt.Print(core.FormatStats(stats))
			return nil
		},
	}
}

func metricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics <run_id>",
		Short: "Compute trace-derived metrics and automatic run classification",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir, err := findRunDir(args[0])
			if err != nil {
				return err
			}
			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			metrics := core.ComputeMetrics(events)
			classification := core.ClassifyRun(events)
			fmt.Print(core.FormatMetrics(metrics, classification))
			return nil
		},
	}
}

func compareCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compare <run_id> <run_id> [run_id...]",
		Short: "Compare statistics across multiple runs",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var allStats []core.RunStats
			for _, id := range args {
				runDir, err := findRunDir(id)
				if err != nil {
					return err
				}
				events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
				if err != nil {
					return err
				}
				s := core.ComputeStats(events)
				if meta, err := core.ReadMeta(runDir); err == nil {
					s.Score = meta.Score
					if s.ModelMetadata == (providers.ModelMetadata{}) {
						s.ModelMetadata = meta.ModelMetadata
					}
				}
				allStats = append(allStats, s)
			}
			fmt.Print(core.FormatCompare(allStats))
			return nil
		},
	}
}

func benchCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "bench <bench.toml>",
		Short: "Run batch evaluation from a bench config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bc, err := core.LoadBenchConfig(args[0])
			if err != nil {
				return err
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			ctx := context.Background()

			for _, variant := range bc.Variants {
				// Build GenParams from variant
				genParams := providers.GenParams{
					Temperature: variant.Temperature,
					TopP:        variant.TopP,
					TopK:        variant.TopK,
					MaxTokens:   variant.MaxTokens,
					Seed:        variant.Seed,
				}

				for pi, prompt := range bc.Prompts {
					fmt.Printf("\n%s  variant=%s  prompt=%d\n",
						ui.Info("bench"), variant.Name, pi+1)

					// Create run
					runID := newRunID()
					runDir := filepath.Join("runs", runID)
					if err := os.MkdirAll(runDir, 0o755); err != nil {
						return err
					}

					meta := core.RunMeta{
						RunID:           runID,
						Name:            bc.Name,
						Tags:            map[string]string{"experiment": bc.Name, "variant": variant.Name},
						Provider:        variant.Provider,
						Model:           variant.Model,
						SourceWorkspace: ".",
						CreatedAt:       time.Now().UTC(),
					}
					_ = core.WriteMeta(runDir, meta)

					tracePath := filepath.Join(runDir, "trace.jsonl")
					trace, err := core.OpenTrace(tracePath)
					if err != nil {
						return err
					}

					coreRun := &core.Run{ID: runID, Dir: runDir, TraceFile: tracePath}

					// Build sandbox session
					var s_session executor.Session
					var s_mapper *core.PathMapper
					var s_workspace string
					s_session, s_mapper, s_workspace, err = buildSandboxSession(cfg, runID, ".", "runs")
					if err != nil {
						trace.Close()
						return err
					}
					if cfg.Sandbox.Enabled {
						defer s_session.Close()
					}

					reg := buildToolRegistry(cfg)
					pol := loadPolicy(cfg, "default")

					// Build provider from variant config
					pc, ok := cfg.Providers[variant.Provider]
					if !ok {
						trace.Close()
						return fmt.Errorf("provider %q not configured", variant.Provider)
					}
					if variant.Model != "" {
						pc.DefaultModel = variant.Model
					}
					prov, err := buildProviderFromConfig(pc)
					if err != nil {
						trace.Close()
						return err
					}

					// Resolve solver
					var solver core.Solver
					solverName := variant.Solver
					if solverName == "" {
						solverName = cfg.Defaults.Solver
					}
					switch solverName {
					case "plan_execute":
						maxReplans := cfg.Defaults.MaxReplans
						if maxReplans <= 0 {
							maxReplans = 3
						}
						solver = &core.PlanExecuteSolver{MaxReplans: maxReplans}
					case "react", "":
						solver = &core.ReactSolver{}
					default:
						trace.Close()
						return fmt.Errorf("variant %s: unknown solver %q", variant.Name, solverName)
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
					confirmFn := func(_, _ string) bool { return true } // auto-approve
					outputFn := core.OutputFn(renderer.RenderEvent)
					registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, s_workspace, pol.MaxToolCallsPerStep, s_session, s_mapper)

					loop := &core.Loop{
						Run:       coreRun,
						Provider:  prov,
						Tools:     reg,
						Policy:    pol,
						Trace:     trace,
						Budget:    budget,
						ConfirmFn: confirmFn,
						OutputFn:  outputFn,
						GenParams: genParams,
						Solver:    solver,
						Session:   s_session,
						Mapper:    s_mapper,
						NetworkTier: loopNetworkTier(cfg),
						Snapshots:   buildSnapshotManager(cfg, s_workspace),
					}

					metadata, _ := prov.Metadata(ctx, variant.Model)
					loop.ModelMetadata = metadata

					_ = loop.EmitRunStart(core.RunStartPayload{
						Policy:        pol.Name,
						Provider:      prov.Name(),
						Model:         variant.Model,
						Workspace:     s_workspace,
						ModelMetadata: metadata,
					})

					reason := "completed"
					if err := loop.Step(ctx, prompt.Message); err != nil {
						reason = "error"
					}
					_ = loop.EmitRunEnd(reason)
					trace.Close()
				}
			}

			fmt.Printf("\n%s\n", ui.Header("Benchmark complete"))
			return nil
		},
	}
}

func queryCmd() *cobra.Command {
	var tagFilter []string
	var scoreFilter string

	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query runs by tags, score, or name",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := os.ReadDir("runs")
			if err != nil {
				return fmt.Errorf("cannot read runs/: %w", err)
			}

			wantTags := parseTags(tagFilter)

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				dir := filepath.Join("runs", entry.Name())
				meta, err := core.ReadMeta(dir)
				if err != nil {
					continue
				}

				// Filter by score
				if scoreFilter != "" && meta.Score != scoreFilter {
					continue
				}

				// Filter by tags
				match := true
				for k, v := range wantTags {
					if meta.Tags[k] != v {
						match = false
						break
					}
				}
				if !match {
					continue
				}

				score := meta.Score
				if score == "" {
					score = "-"
				}
				fmt.Printf("%-28s  %-10s %-12s %-8s %-10s %-12s %s\n",
					meta.RunID,
					meta.Provider,
					meta.Model,
					core.FormatContextSize(meta.ModelMetadata.ContextSize),
					core.FormatModelPricing(meta.ModelMetadata),
					score,
					meta.Name,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&tagFilter, "tag", nil, "filter by tag key=value (repeatable)")
	cmd.Flags().StringVar(&scoreFilter, "score", "", "filter by score (pass|fail|partial)")
	return cmd
}

func experimentCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "experiment",
		Short: "Manage research experiments",
	}

	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new research experiment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repeats, _ := cmd.Flags().GetInt("repeats")
			variantsStr, _ := cmd.Flags().GetStringSlice("variants") // model:solver format

			var variants []eval.Variant
			for _, v := range variantsStr {
				parts := strings.Split(v, ":")
				if len(parts) != 2 {
					return fmt.Errorf("invalid variant format %q, use model:solver", v)
				}
				variants = append(variants, eval.Variant{
					Name:   v,
					Model:  parts[0],
					Solver: parts[1],
				})
			}

			cfg := eval.ExperimentConfig{
				Variants: variants,
				Repeats:  repeats,
			}
			exp := eval.NewExperiment(name, cfg)
			if err := exp.Save("runs"); err != nil {
				return err
			}
			fmt.Printf("Created experiment: %s\n", exp.ID)
			return nil
		},
	}
	create.Flags().Int("repeats", 3, "number of trials per variant")
	create.Flags().StringSlice("variants", nil, "variants in model:solver format")

	run := &cobra.Command{
		Use:   "run <experiment_id> --prompt <prompt>",
		Short: "Execute all variants × repeats for an experiment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt, _ := cmd.Flags().GetString("prompt")
			if prompt == "" {
				return fmt.Errorf("--prompt is required")
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			exp, err := eval.LoadExperiment("runs", args[0])
			if err != nil {
				return err
			}
			exp.Status = "running"
			_ = exp.Save("runs")

			total := len(exp.Config.Variants) * exp.Config.Repeats
			completed := 0

			for _, variant := range exp.Config.Variants {
				provName := variant.Provider
				if provName == "" {
					provName = cfg.Defaults.Provider
				}
				prov, err := buildProvider(cfg, provName)
				if err != nil {
					return fmt.Errorf("variant %s: %w", variant.Name, err)
				}

				// Resolve model
				model := variant.Model
				if model == "" {
					if pc, ok := cfg.Providers[provName]; ok {
						model = pc.DefaultModel
					}
				}

				// Resolve solver
				var solver core.Solver
				solverName := variant.Solver
				if solverName == "" {
					solverName = cfg.Defaults.Solver
				}
				switch solverName {
				case "plan_execute":
					maxReplans := cfg.Defaults.MaxReplans
					if maxReplans <= 0 {
						maxReplans = 3
					}
					solver = &core.PlanExecuteSolver{MaxReplans: maxReplans}
				case "react", "":
					solver = &core.ReactSolver{}
				default:
					return fmt.Errorf("variant %s: unknown solver %q", variant.Name, solverName)
				}

				for r := 0; r < exp.Config.Repeats; r++ {
					completed++
					fmt.Printf("[%d/%d] variant=%s repeat=%d/%d\n", completed, total, variant.Name, r+1, exp.Config.Repeats)

					runID := newRunID()
					runDir := filepath.Join("runs", runID)
					if err := os.MkdirAll(runDir, 0o755); err != nil {
						return err
					}

					tags := map[string]string{
						"experiment": exp.ID,
						"variant":    variant.Name,
						"repeat":     fmt.Sprintf("%d", r+1),
					}
					meta := core.RunMeta{
						RunID:           runID,
						Name:            fmt.Sprintf("%s/%s/%d", exp.Name, variant.Name, r+1),
						Tags:            tags,
						Provider:        provName,
						Model:           model,
						SourceWorkspace: ".",
						CreatedAt:       time.Now().UTC(),
					}
					_ = core.WriteMeta(runDir, meta)

					tracePath := filepath.Join(runDir, "trace.jsonl")
					trace, err := core.OpenTrace(tracePath)
					if err != nil {
						return err
					}

					coreRun := &core.Run{
						ID:        runID,
						Dir:       runDir,
						TraceFile: tracePath,
						Budget: core.Budget{
							MaxSteps:   cfg.Defaults.BudgetSteps,
							MaxTokens:  cfg.Defaults.BudgetTokens,
							MaxCostUSD: cfg.Defaults.BudgetCostUSD,
						},
					}

					reg := buildToolRegistry(cfg)
					pol := loadPolicy(cfg, "default")
					budget := core.NewBudgetTracker(&coreRun.Budget)

					// Build sandbox session
					var s_session executor.Session
					var s_mapper *core.PathMapper
					var s_workspace string
					s_session, s_mapper, s_workspace, err = buildSandboxSession(cfg, runID, ".", "runs")
					if err != nil {
						trace.Close()
						return err
					}
					if cfg.Sandbox.Enabled {
						defer s_session.Close()
					}

					confirmFn := func(_, _ string) bool { return true } // auto-approve for experiments
					outputFn := core.OutputFn(func(ev core.Event) {})   // silent by default
					registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, s_workspace, pol.MaxToolCallsPerStep, s_session, s_mapper)

					loop := &core.Loop{
						Run:       coreRun,
						Provider:  prov,
						Tools:     reg,
						Policy:    pol,
						Trace:     trace,
						Budget:    budget,
						ConfirmFn: confirmFn,
						OutputFn:  outputFn,
						Solver:    solver,
						GenParams: providers.GenParams{},
						Session:   s_session,
						Mapper:    s_mapper,
						NetworkTier: loopNetworkTier(cfg),
						Snapshots:   buildSnapshotManager(cfg, s_workspace),
					}

					metadata, _ := prov.Metadata(context.Background(), model)
					loop.ModelMetadata = metadata

					_ = loop.EmitRunStart(core.RunStartPayload{
						Policy:        "default",
						Provider:      provName,
						Model:         model,
						Workspace:     s_workspace,
						ModelMetadata: metadata,
					})

					err = loop.Step(context.Background(), prompt)
					reason := "completed"
					if err != nil {
						reason = "error"
						fmt.Printf("  warning: run %s ended with error: %v\n", runID, err)
					}
					_ = loop.EmitRunEnd(reason)
					trace.Close()

					exp.RunIDs = append(exp.RunIDs, runID)
					_ = exp.Save("runs")
				}
			}

			exp.Status = "completed"
			_ = exp.Save("runs")
			fmt.Printf("\nExperiment %s completed. %d runs recorded.\n", exp.ID, len(exp.RunIDs))
			fmt.Printf("View results: v100 experiment results %s\n", exp.ID)
			return nil
		},
	}
	run.Flags().String("prompt", "", "prompt to send to each variant trial")

	results := &cobra.Command{
		Use:   "results <experiment_id>",
		Short: "Display statistical results for an experiment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			exp, err := eval.LoadExperiment("runs", args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Experiment: %s (%s)\n", exp.Name, exp.ID)
			fmt.Printf("Status: %s | Variants: %d | Repeats: %d | Runs: %d\n\n",
				exp.Status, len(exp.Config.Variants), exp.Config.Repeats, len(exp.RunIDs))

			// Group run IDs by variant using tags
			variantRuns := map[string][]string{}
			for _, runID := range exp.RunIDs {
				meta, err := core.ReadMeta(filepath.Join("runs", runID))
				if err != nil {
					continue
				}
				vName := meta.Tags["variant"]
				if vName == "" {
					vName = "unknown"
				}
				variantRuns[vName] = append(variantRuns[vName], runID)
			}

			// Compute and display per-variant statistics
			for _, variant := range exp.Config.Variants {
				runIDs := variantRuns[variant.Name]
				if len(runIDs) == 0 {
					fmt.Printf("Variant: %s — no runs found\n\n", variant.Name)
					continue
				}

				var metrics []core.RunMetrics
				for _, runID := range runIDs {
					events, err := core.ReadAll(filepath.Join("runs", runID, "trace.jsonl"))
					if err != nil {
						continue
					}
					metrics = append(metrics, core.ComputeMetrics(events))
				}

				stats := eval.AggregateResults(variant.Name, metrics)
				fmt.Printf("Variant: %s\n", ui.Info(stats.VariantName))
				fmt.Printf("  Trials:      %d\n", stats.Trials)
				fmt.Printf("  Pass Rate:   %.1f%% [95%% CI: %.1f%%–%.1f%%]\n",
					stats.PassRate*100, stats.CI95Low*100, stats.CI95High*100)
				fmt.Printf("  Mean Tokens: %.0f\n", stats.MeanTokens)
				fmt.Printf("  Mean Cost:   $%.4f\n", stats.MeanCost)
				fmt.Printf("  Mean Steps:  %.1f\n\n", stats.MeanSteps)
			}
			return nil
		},
	}

	cmd.AddCommand(create, run, results)
	return cmd
}

func analyzeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "analyze <run_id>",
		Short: "Perform automated behavioral analysis on a run trace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}
			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}

			report := eval.AnalyzeTrajectory(events)

			fmt.Printf("Analysis for Run: %s\n", ui.Info(runID))
			fmt.Printf("Efficiency Score: %.2f\n", report.Efficiency)
			fmt.Printf("Tool Errors:      %d\n", report.ToolErrors)
			fmt.Println("\nBehavioral Labels:")
			if len(report.Labels) == 0 {
				fmt.Println("  (Normal behavior detected)")
			}
			for _, l := range report.Labels {
				fmt.Printf("  [%s] %s (conf: %.2f)\n", ui.Warn(l.Name), l.Evidence, l.Confidence)
			}
			return nil
		},
	}
}

func diffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <run_id_a> <run_id_b>",
		Short: "Find the point of divergence between two run traces",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runA := args[0]
			runB := args[1]

			eventsA, err := core.ReadAll(filepath.Join("runs", runA, "trace.jsonl"))
			if err != nil {
				return err
			}
			eventsB, err := core.ReadAll(filepath.Join("runs", runB, "trace.jsonl"))
			if err != nil {
				return err
			}

			diff := eval.DiffTraces(runA, runB, eventsA, eventsB)

			fmt.Printf("Comparing %s vs %s\n", ui.Info(runA), ui.Info(runB))
			if diff.DivergeType == "none" {
				fmt.Println(ui.OK("No divergence detected. Traces are structurally identical."))
				return nil
			}

			fmt.Printf("Divergence Type: %s\n", ui.Warn(diff.DivergeType))
			fmt.Printf("Common Prefix:   %d events\n", diff.CommonPrefix)
			fmt.Printf("Evidence:        %s\n", diff.DiffEvidence)

			return nil
		},
	}
}

func verifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <run_id> [bench.toml|experiment.json]",
		Short: "Automatically verify a run result against success invariants",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			var invariants []eval.SuccessInvariant

			// 1. Try to load invariants from bench or experiment config if provided
			if len(args) > 1 {
				configPath := args[1]
				if strings.HasSuffix(configPath, ".toml") {
					bc, err := core.LoadBenchConfig(configPath)
					if err == nil {
						for _, inv := range bc.Invariants {
							invariants = append(invariants, eval.SuccessInvariant{
								Type:    inv.Type,
								Path:    inv.Path,
								Pattern: inv.Pattern,
								Hash:    inv.Hash,
							})
						}
					}
				} else if strings.HasSuffix(configPath, ".json") {
					exp, err := eval.LoadExperiment("runs", configPath)
					if err == nil {
						invariants = exp.Config.Invariants
					}
				}
			}

			if len(invariants) == 0 {
				return fmt.Errorf("no invariants provided and none found in run metadata")
			}

			fmt.Printf("Verifying %s against %d invariants...\n", ui.Info(runID), len(invariants))

			meta, err := core.ReadMeta(runDir)
			if err != nil {
				return err
			}

			// 2. Load workspace path from meta
			workspace := meta.SourceWorkspace
			if workspace == "" {
				workspace = "."
			}

			// 3. Perform physical verification
			passed := true
			var evidence []string

			for _, inv := range invariants {
				fullPath := filepath.Join(workspace, inv.Path)
				switch inv.Type {
				case "file_exists":
					if _, err := os.Stat(fullPath); err != nil {
						passed = false
						evidence = append(evidence, fmt.Sprintf("FAIL: file %s does not exist", inv.Path))
					} else {
						evidence = append(evidence, fmt.Sprintf("PASS: file %s exists", inv.Path))
					}
				case "no_file":
					if _, err := os.Stat(fullPath); err == nil {
						passed = false
						evidence = append(evidence, fmt.Sprintf("FAIL: file %s exists but should not", inv.Path))
					} else {
						evidence = append(evidence, fmt.Sprintf("PASS: file %s is absent", inv.Path))
					}
				case "file_contains":
					data, err := os.ReadFile(fullPath)
					if err != nil {
						passed = false
						evidence = append(evidence, fmt.Sprintf("FAIL: could not read %s: %v", inv.Path, err))
					} else if !strings.Contains(string(data), inv.Pattern) {
						passed = false
						evidence = append(evidence, fmt.Sprintf("FAIL: %s does not contain pattern %q", inv.Path, inv.Pattern))
					} else {
						evidence = append(evidence, fmt.Sprintf("PASS: %s contains pattern %q", inv.Path, inv.Pattern))
					}
				}
			}

			// 4. Update score
			score := "fail"
			if passed {
				score = "pass"
				fmt.Println(ui.OK("Verification PASSED"))
			} else {
				fmt.Println(ui.Fail("Verification FAILED"))
			}

			for _, e := range evidence {
				fmt.Println("  " + e)
			}

			meta.Score = score
			meta.ScoreNotes = strings.Join(evidence, " | ")
			return core.WriteMeta(runDir, meta)
		},
	}
}

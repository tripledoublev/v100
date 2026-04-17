package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/compute"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func researchCmd(cfgPath *string) *cobra.Command {
	var (
		researchConfigPath string
		providerName       string
		computeName        string
		maxRounds          int
		dryRun             bool
	)

	cmd := &cobra.Command{
		Use:   "research",
		Short: "Run autonomous experiment loops inside v100",
		RunE: func(cmd *cobra.Command, args []string) error {
			if researchConfigPath == "" {
				return fmt.Errorf("--config is required")
			}

			// Load research config
			researchCfg, err := core.LoadResearchConfig(researchConfigPath)
			if err != nil {
				return err
			}

			// Load v100 config
			v100cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			// Resolve provider
			if providerName == "" {
				providerName = v100cfg.Defaults.Provider
			}
			prov, err := buildProvider(v100cfg, providerName)
			if err != nil {
				return fmt.Errorf("build provider: %w", err)
			}

			// Verify we're in a git repo
			if _, err := exec.Command("git", "rev-parse", "--git-dir").Output(); err != nil {
				return fmt.Errorf("not in a git repo: %w", err)
			}

			// Check for dirty working directory (other tracked changes)
			out, _ := exec.Command("git", "status", "--short").Output()
			if len(out) > 0 && !bytes.Contains(out, []byte(researchCfg.Target.File)) {
				fmt.Printf("%s\n", ui.Warn("⚠ WARNING: Dirty working directory detected"))
				fmt.Printf("  The agent can run arbitrary shell commands and modify any file.\n")
				fmt.Printf("  Commit or stash all unrelated changes before proceeding.\n")
				fmt.Printf("  Otherwise, git commit -a may sweep them up.\n\n")
			}

			// Build compute provider (flag overrides toml)
			computeCfg := compute.Config{
				Provider:    researchCfg.Compute.Provider,
				GPU:         researchCfg.Compute.GPU,
				Image:       researchCfg.Compute.Image,
				Timeout:     researchCfg.Compute.Timeout,
				ModalSecret: researchCfg.Compute.ModalSecret,
			}
			if computeName != "" {
				computeCfg.Provider = computeName
			}
			computeProv, err := compute.Build(computeCfg)
			if err != nil {
				return fmt.Errorf("build compute provider: %w", err)
			}

			// Run the research loop
			return runResearchLoop(context.Background(), researchCfg, v100cfg, prov, providerName, computeProv, maxRounds, dryRun)
		},
	}

	cmd.Flags().StringVar(&researchConfigPath, "config", "", "path to research.toml (required)")
	cmd.Flags().StringVar(&providerName, "provider", "", "LLM provider for agent")
	cmd.Flags().StringVar(&computeName, "compute", "", "compute provider for experiments (local, modal)")
	cmd.Flags().IntVar(&maxRounds, "max-rounds", 0, "max rounds (0 = unlimited)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would run without executing")

	return cmd
}

func runResearchLoop(
	ctx context.Context,
	researchCfg *core.ResearchConfig,
	v100cfg *config.Config,
	prov providers.Provider,
	providerName string,
	computeProv compute.Provider,
	maxRounds int,
	dryRun bool,
) error {
	fmt.Printf("%s\n", ui.Header("Research Loop"))
	fmt.Printf("  Project:    %s\n", researchCfg.Name)
	fmt.Printf("  Provider:   %s\n", providerName)
	fmt.Printf("  Compute:    %s\n", computeProv.Name())
	fmt.Printf("  Target:     %s\n", researchCfg.Target.File)
	fmt.Printf("  Metric:     %s (lower = better: %v)\n", researchCfg.Experiment.Metric, researchCfg.Experiment.Direction == "lower")
	fmt.Printf("  Timeout:    %s\n", researchCfg.Experiment.Timeout)
	fmt.Printf("  Command:    %s\n\n", researchCfg.Experiment.Command)

	if dryRun {
		fmt.Printf("%s  Dry run mode enabled\n", ui.Info("note"))
		return nil
	}

	// Read context files
	contextContent, err := readContextFiles(researchCfg.Target.Context)
	if err != nil {
		return err
	}

	// Read program
	programContent, err := os.ReadFile(researchCfg.Target.Program)
	if err != nil {
		return fmt.Errorf("read program: %w", err)
	}

	// Read current target file for baseline
	targetContent, err := os.ReadFile(researchCfg.Target.File)
	if err != nil {
		return fmt.Errorf("read target file: %w", err)
	}

	// Initialize results file
	resultsFile := "results.tsv"
	var resultsInitialized bool
	if _, err := os.Stat(resultsFile); err == nil {
		resultsInitialized = true
	}

	// Run baseline experiment
	fmt.Printf("%s\n", ui.Header("Baseline"))
	baselineCommit, _ := gitCurrentCommit()
	baselineBranch, _ := gitCurrentBranch()
	baselineMetric, _, baselineStatus, err := runExperiment(ctx, researchCfg, computeProv, 0, baselineCommit, baselineBranch, "baseline")
	if err != nil {
		return fmt.Errorf("baseline experiment: %w", err)
	}

	if !resultsInitialized {
		header := fmt.Sprintf("commit\t%s\tmemory_gb\tstatus\tdescription\n", researchCfg.Experiment.Metric)
		if err := os.WriteFile(resultsFile, []byte(header), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("  %s  Baseline: %s (status: %s)\n", ui.OK("recorded"), formatMetric(baselineMetric), baselineStatus)

	// Append baseline to TSV
	if baselineStatus != "crash" {
		if err := appendResults(resultsFile, core.ResearchResult{
			Commit:      baselineCommit,
			Metric:      baselineMetric,
			Status:      baselineStatus,
			Description: "baseline",
		}); err != nil {
			fmt.Printf("  %s  Failed to append baseline result: %v\n", ui.Warn("warn"), err)
		}
	} else {
		// Baseline crashed: cannot establish a meaningful starting point
		fmt.Printf("%s  Baseline crashed or metric missing. Cannot continue.\n", ui.Fail("fatal"))
		return fmt.Errorf("baseline experiment failed (status=%s, metric=%s)", baselineStatus, formatMetric(baselineMetric))
	}

	// Main loop
	round := 0
	bestMetric := baselineMetric
	for {
		round++
		if maxRounds > 0 && round > maxRounds {
			fmt.Printf("%s  Max rounds (%d) reached\n", ui.Info("done"), maxRounds)
			break
		}

		fmt.Printf("\n%s\n", ui.Header(fmt.Sprintf("Round %d", round)))

		// Build agent prompt
		resultsHistory := readResultsHistory(resultsFile)
		agentPrompt := buildAgentPrompt(
			string(programContent),
			contextContent,
			researchCfg.Target.File,
			string(targetContent),
			resultsHistory,
		)

		// Create run for agent
		runID := newRunID()
		runDir := filepath.Join("runs", runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			return err
		}

		traceFile := filepath.Join(runDir, "trace.jsonl")
		trace, err := core.OpenTrace(traceFile)
		if err != nil {
			return err
		}

		// Set up tools (limited set for research).
		// NOTE: fs_write and sh are unrestricted; agent can modify any file or run any command.
		// For safety, run research in a clean repo with NO OTHER TRACKED CHANGES.
		reg := tools.NewRegistry([]string{"fs_read", "fs_write", "sh"})
		reg.Register(tools.FSRead())
		reg.Register(tools.FSWrite())
		reg.Register(tools.Sh())

		// Create loop for agent
		coreRun := &core.Run{ID: runID, Dir: runDir, TraceFile: traceFile}
		budget := core.NewBudgetTracker(&core.Budget{
			MaxSteps:   researchCfg.Budget.Steps,
			MaxTokens:  0, // no token limit
			MaxCostUSD: researchCfg.Budget.CostUSD,
		})

		renderer := ui.NewCLIRenderer()
		confirmFn := func(_, _ string) bool { return true }
		outputFn := core.OutputFn(renderer.RenderEvent)

		pol := loadPolicy(v100cfg, "default")
		solver, err := buildSolver(v100cfg, "")
		if err != nil {
			_ = trace.Close()
			return err
		}

		model := resolveModel(v100cfg, providerName)
		metadata := resolveProviderMetadata(ctx, prov, model, providers.ModelMetadata{})

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
		}
		loop.ModelMetadata = metadata

		_ = loop.EmitRunStart(core.RunStartPayload{
			Policy:        pol.Name,
			Provider:      prov.Name(),
			Model:         model,
			Workspace:     ".",
			ModelMetadata: metadata,
		})

		// Agent makes a proposal
		fmt.Printf("  %s  Agent proposing change...\n", ui.Info("agent"))
		if err := loop.Step(ctx, agentPrompt); err != nil {
			_ = loop.EmitRunEnd("error", "")
			_ = trace.Close()
			// Don't fail the entire research run if one round fails
			fmt.Printf("  %s  Agent proposal failed: %v\n", ui.Fail("error"), err)
			continue
		}
		_ = loop.EmitRunEnd("completed", "")
		_ = trace.Close()

		// Try to get the commit message from the agent's output
		description := extractCommitMessage(runDir)
		if description == "" {
			description = fmt.Sprintf("round %d", round)
		}

		// Commit the change
		currentTarget, _ := os.ReadFile(researchCfg.Target.File)
		if bytes.Equal(currentTarget, targetContent) {
			fmt.Printf("  %s  No changes made by agent\n", ui.Warn("skip"))
			continue
		}

		if err := gitCommit(description); err != nil {
			fmt.Printf("  %s  Git commit failed: %v\n", ui.Fail("error"), err)
			continue
		}
		fmt.Printf("  %s  Committed: %s\n", ui.OK("commit"), description)

		// Run experiment
		fmt.Printf("  %s  Running experiment...\n", ui.Info("run"))
		commitBeforeRun, _ := gitCurrentCommit()
		branchBeforeRun, _ := gitCurrentBranch()
		metric, memGB, status, err := runExperiment(ctx, researchCfg, computeProv, round, commitBeforeRun, branchBeforeRun, runID)
		if err != nil {
			fmt.Printf("  %s  Experiment error: %v\n", ui.Fail("error"), err)
			status = "crash"
		}

		// Decide: keep or discard
		improved := isImproved(metric, bestMetric, researchCfg.Experiment.Direction)
		decision := "discard"
		if status == "crash" {
			decision = "crash"
		} else if improved {
			decision = "keep"
			bestMetric = metric
		}

		commit, _ := gitCurrentCommit()
		result := core.ResearchResult{
			Commit:      commit,
			Metric:      metric,
			MemoryGB:    memGB,
			Status:      decision,
			Description: description,
		}
		if err := appendResults(resultsFile, result); err != nil {
			fmt.Printf("  %s  Failed to append result: %v\n", ui.Warn("warn"), err)
		}

		// Print summary
		switch decision {
		case "keep":
			fmt.Printf("  %s  Metric: %s (improved from %s)\n", ui.OK("keep"), formatMetric(metric), formatMetric(bestMetric))
			// Refresh targetContent for next round (agent needs current state)
			if newContent, err := os.ReadFile(researchCfg.Target.File); err == nil {
				targetContent = newContent
			} else {
				fmt.Printf("  %s  Failed to refresh target file: %v\n", ui.Warn("warn"), err)
			}
		case "crash":
			fmt.Printf("  %s  Crashed (metric: %s, mem: %.1f GB)\n", ui.Fail("crash"), formatMetric(metric), memGB)
		default:
			fmt.Printf("  %s  Metric: %s (no improvement, reverting)\n", ui.Warn("discard"), formatMetric(metric))
		}

		if decision != "keep" {
			if err := gitResetTargetFile(researchCfg.Target.File); err != nil {
				fmt.Printf("  %s  Git reset failed: %v\n", ui.Fail("error"), err)
				// Continue anyway
			} else {
				fmt.Printf("  %s  Reverted %s to previous commit\n", ui.Info("reset"), researchCfg.Target.File)
			}
			if newContent, err := os.ReadFile(researchCfg.Target.File); err == nil {
				targetContent = newContent
			}
		}
	}

	fmt.Printf("\n%s\n", ui.Header("Research Complete"))
	fmt.Printf("  Results saved to: %s\n", resultsFile)
	fmt.Printf("  Best metric: %s\n", formatMetric(bestMetric))

	return nil
}

// readContextFiles reads all context files into a single string.
func readContextFiles(paths []string) (string, error) {
	var buf bytes.Buffer
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		fmt.Fprintf(&buf, "=== %s ===\n%s\n\n", path, string(content))
	}
	return buf.String(), nil
}

// buildAgentPrompt constructs the system prompt for the agent.
func buildAgentPrompt(program, context, targetFile, targetContent, resultsHistory string) string {
	return fmt.Sprintf(`%s

%s

=== Current Target File: %s ===
%s

=== Results History ===
%s

Please analyze the current code and results history, and propose your next experiment. Modify %s to test your hypothesis.
`, program, context, targetFile, targetContent, resultsHistory, targetFile)
}

// runExperiment executes the experiment command and returns the metric.
func runExperiment(ctx context.Context, cfg *core.ResearchConfig, computeProv compute.Provider, round int, commit, branch, runID string) (float64, float64, string, error) {
	res, err := core.RunResearchExperiment(ctx, cfg, core.ExperimentRunContext{
		Round:      round,
		RunID:      runID,
		Commit:     commit,
		Branch:     branch,
		Workspace:  ".",
		TargetFile: cfg.Target.File,
		MetricName: cfg.Experiment.Metric,
		Timestamp:  time.Now(),
	}, computeProv)
	if err != nil {
		return math.NaN(), 0, "crash", err
	}
	combined := strings.TrimSpace(res.Output)
	if combined != "" {
		fmt.Println(combined)
	}
	if strings.TrimSpace(res.LocalLog) != "" && strings.TrimSpace(res.LocalLog) != combined {
		fmt.Println(strings.TrimSpace(res.LocalLog))
	}
	metric := res.Metric
	if res.Status == "crash" && metric == 0 {
		metric = math.NaN()
	}
	return metric, res.MemoryGB, res.Status, nil
}

// isImproved checks if the new metric is better than the baseline.
func isImproved(newMetric, baselineMetric float64, direction string) bool {
	if math.IsNaN(newMetric) || math.IsNaN(baselineMetric) {
		return false
	}
	if direction == "lower" {
		return newMetric < baselineMetric
	}
	return newMetric > baselineMetric
}

// formatMetric returns a formatted metric string.
func formatMetric(m float64) string {
	if math.IsNaN(m) {
		return "N/A"
	}
	return fmt.Sprintf("%.6f", m)
}

// gitCurrentCommit returns the short commit hash.
func gitCurrentCommit() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitCurrentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCommit commits with the given message.
func gitCommit(message string) error {
	cmd := exec.Command("git", "commit", "-a", "-m", message)
	return cmd.Run()
}

// gitResetTargetFile reverts only the target file to HEAD (safe, no whole-tree reset).
func gitResetTargetFile(filePath string) error {
	// Use git checkout to revert only this file
	cmd := exec.Command("git", "checkout", "HEAD", "--", filePath)
	return cmd.Run()
}

// readResultsHistory reads the TSV file and formats it for the agent.
func readResultsHistory(filePath string) string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return string(content)
}

// extractCommitMessage tries to extract a commit description from the agent's trace.
func extractCommitMessage(runDir string) string {
	tracePath := filepath.Join(runDir, "trace.jsonl")
	events, err := core.ReadAll(tracePath)
	if err != nil {
		return ""
	}

	// Look for the agent's text response
	for _, ev := range events {
		if ev.Type == core.EventModelResp {
			var payload core.ModelRespPayload
			if err := json.Unmarshal(ev.Payload, &payload); err == nil {
				if payload.Text != "" {
					// Extract first line as commit message
					lines := strings.Split(payload.Text, "\n")
					for _, line := range lines {
						line = strings.TrimSpace(line)
						if len(line) > 0 && !strings.HasPrefix(line, "#") {
							if len(line) > 80 {
								line = line[:80]
							}
							return line
						}
					}
				}
			}
		}
	}
	return ""
}

// appendResults appends a result to the TSV file.
func appendResults(filePath string, result core.ResearchResult) error {
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	line := fmt.Sprintf("%s\t%.6f\t%.1f\t%s\t%s\n",
		result.Commit,
		result.Metric,
		result.MemoryGB,
		result.Status,
		result.Description,
	)
	_, err = f.WriteString(line)
	return err
}

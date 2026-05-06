//go:build !windows
// +build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

var wakeExecCommand = defaultWakeExecCommand

type wakeIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	State  string `json:"state,omitempty"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels,omitempty"`
}

type wakeGoalRunPayload struct {
	GoalID          string    `json:"goal_id,omitempty"`
	Goal            string    `json:"goal"`
	PromptPath      string    `json:"prompt_path"`
	RunCommand      string    `json:"run_command"`
	Provider        string    `json:"provider"`
	Solver          string    `json:"solver"`
	BudgetSteps     int       `json:"budget_steps,omitempty"`
	BudgetTokens    int       `json:"budget_tokens,omitempty"`
	MaxToolCalls    int       `json:"max_tool_calls_per_step,omitempty"`
	EnabledTools    []string  `json:"enabled_tools,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	SourceWorkspace string    `json:"source_workspace,omitempty"`
}

func wakeCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Run autonomous wake cycles on a recurring schedule",
		Long:  "Run autonomous wake cycles on a recurring schedule. Each cycle creates a real run artifact and records one next-step goal for the workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		wakeStartCmd(cfgPath),
		wakeRunCmd(cfgPath),
		wakeStatusCmd(cfgPath),
		wakeStopCmd(cfgPath),
		wakeTaskCmd(cfgPath),
		wakeGoalsCmd(cfgPath),
		wakeEvolveCmd(cfgPath),
	)

	return cmd
}

func wakeGoalsCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "goals",
		Short: "Review queued autonomous wake goals",
		RunE: func(cmd *cobra.Command, args []string) error {
			return wakeGoalsList()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List queued wake goals",
			RunE: func(cmd *cobra.Command, args []string) error {
				return wakeGoalsList()
			},
		},
		&cobra.Command{
			Use:   "approve <id-or-index>",
			Short: "Approve a queued goal by moving it to the front",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return updateWakeGoalQueue(args[0], func(state *core.WakeState, idx int) string {
					goal := state.QueuedGoals[idx]
					copy(state.QueuedGoals[1:idx+1], state.QueuedGoals[0:idx])
					state.QueuedGoals[0] = goal
					return fmt.Sprintf("approved wake goal %s", wakeGoalDisplayID(goal, 0))
				})
			},
		},
		&cobra.Command{
			Use:   "reject <id-or-index>",
			Short: "Reject and remove a queued goal",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return updateWakeGoalQueue(args[0], func(state *core.WakeState, idx int) string {
					goal := state.QueuedGoals[idx]
					state.QueuedGoals = append(state.QueuedGoals[:idx], state.QueuedGoals[idx+1:]...)
					return fmt.Sprintf("rejected wake goal %s", wakeGoalDisplayID(goal, idx))
				})
			},
		},
		&cobra.Command{
			Use:   "edit <id-or-index> <content>",
			Short: "Edit a queued goal",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				content := strings.TrimSpace(args[1])
				if content == "" {
					return errors.New("edited goal content cannot be empty")
				}
				return updateWakeGoalQueue(args[0], func(state *core.WakeState, idx int) string {
					state.QueuedGoals[idx].Content = content
					state.QueuedGoals[idx] = core.ScoreGeneratedGoal(state.QueuedGoals[idx])
					state.QueuedGoals = core.RankGeneratedGoals(state.QueuedGoals)
					return "edited wake goal"
				})
			},
		},
		&cobra.Command{
			Use:   "handoff <id-or-index>",
			Short: "Create an executable run payload from a queued goal",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig(*cfgPath)
				if err != nil {
					return err
				}
				runID, payload, err := createWakeGoalRunPayload(cfg, args[0])
				if err != nil {
					return err
				}
				fmt.Printf("✓ created wake goal run payload %s\n", runID)
				fmt.Printf("  prompt:  %s\n", payload.PromptPath)
				fmt.Printf("  command: %s\n", payload.RunCommand)
				return nil
			},
		},
	)
	return cmd
}

func wakeEvolveCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolve",
		Short: "Apply completed evolution runs under wake control",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "adopt <evolve_id>",
		Short: "Adopt a completed evolution with rollback on failure",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			evolveID := args[0]
			evolveDir, err := findRunDir(evolveID)
			if err != nil {
				return fmt.Errorf("find evolve run: %w", err)
			}
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
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			applyReport, err := applyEvolutionWithRollback(cfg, evolveID, report.CandidatePath, "")
			if err != nil {
				return err
			}
			fmt.Printf("%s  wake adopted %s -> %s\n", ui.OK("done"), evolveID, applyReport.TargetPath)
			return nil
		},
	})
	return cmd
}

func wakeGoalsList() error {
	state, err := core.ReadWakeState(core.DefaultWakeStatePath())
	if err != nil {
		fmt.Println("wake goals  none")
		return nil
	}
	if len(state.QueuedGoals) == 0 {
		fmt.Println("wake goals  none")
		return nil
	}
	for i, goal := range state.QueuedGoals {
		fmt.Printf("%d. %s  score=%d  %s\n", i+1, wakeGoalDisplayID(goal, i), goal.Score, goal.Content)
	}
	return nil
}

func updateWakeGoalQueue(selector string, mutate func(*core.WakeState, int) string) error {
	statePath := core.DefaultWakeStatePath()
	state, err := core.ReadWakeState(statePath)
	if err != nil {
		return fmt.Errorf("read wake state: %w", err)
	}
	idx, err := findWakeGoalIndex(state.QueuedGoals, selector)
	if err != nil {
		return err
	}
	message := mutate(state, idx)
	if err := core.WriteWakeState(statePath, state); err != nil {
		return fmt.Errorf("write wake state: %w", err)
	}
	fmt.Printf("✓ %s\n", message)
	return nil
}

func findWakeGoalIndex(goals []core.GeneratedGoal, selector string) (int, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, errors.New("goal selector is required")
	}
	if n, err := strconv.Atoi(selector); err == nil {
		idx := n - 1
		if idx >= 0 && idx < len(goals) {
			return idx, nil
		}
		return 0, fmt.Errorf("goal index %d out of range", n)
	}
	for i, goal := range goals {
		if goal.ID == selector {
			return i, nil
		}
	}
	return 0, fmt.Errorf("wake goal %q not found", selector)
}

func wakeGoalDisplayID(goal core.GeneratedGoal, idx int) string {
	if strings.TrimSpace(goal.ID) != "" {
		return goal.ID
	}
	return fmt.Sprintf("#%d", idx+1)
}

func createWakeGoalRunPayload(cfg *config.Config, selector string) (string, wakeGoalRunPayload, error) {
	state, err := core.ReadWakeState(core.DefaultWakeStatePath())
	if err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("read wake state: %w", err)
	}
	idx, err := findWakeGoalIndex(state.QueuedGoals, selector)
	if err != nil {
		return "", wakeGoalRunPayload{}, err
	}
	goal := core.ScoreGeneratedGoal(state.QueuedGoals[idx])
	workspace, err := os.Getwd()
	if err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("resolve workspace: %w", err)
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	providerName := resolveWakeProvider(cfg, "")
	solverName := strings.TrimSpace(cfg.Defaults.Solver)
	if solverName == "" {
		solverName = "react"
	}
	runID := newRunID()
	runDir := filepath.Join("runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("create handoff run dir: %w", err)
	}
	prompt := buildWakeGoalRunPrompt(goal)
	promptPath := filepath.Join(runDir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("write prompt: %w", err)
	}
	enabledTools := append([]string(nil), cfg.Tools.Enabled...)
	sort.Strings(enabledTools)
	command := fmt.Sprintf("v100 run --exit --solver %s --provider %s --prompt-file %s", shellQuoteArg(solverName), shellQuoteArg(providerName), shellQuoteArg(promptPath))
	payload := wakeGoalRunPayload{
		GoalID:          goal.ID,
		Goal:            goal.Content,
		PromptPath:      promptPath,
		RunCommand:      command,
		Provider:        providerName,
		Solver:          solverName,
		BudgetSteps:     cfg.Defaults.BudgetSteps,
		BudgetTokens:    cfg.Defaults.BudgetTokens,
		MaxToolCalls:    cfg.Defaults.MaxToolCallsPerStep,
		EnabledTools:    enabledTools,
		CreatedAt:       time.Now().UTC(),
		SourceWorkspace: workspace,
	}
	payloadBytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("marshal wake payload: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "wake_payload.json"), payloadBytes, 0o644); err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("write wake payload: %w", err)
	}
	meta := core.RunMeta{
		RunID:           runID,
		Name:            "wake-goal-handoff",
		Tags:            map[string]string{"type": "wake.goal_handoff", "goal_id": goal.ID},
		Provider:        providerName,
		Model:           resolveWakeModel(cfg, providerName),
		SourceWorkspace: workspace,
		CreatedAt:       payload.CreatedAt,
		GeneratedGoals:  []core.GeneratedGoal{goal},
	}
	if err := core.WriteMeta(runDir, meta); err != nil {
		return "", wakeGoalRunPayload{}, fmt.Errorf("write handoff meta: %w", err)
	}
	return runID, payload, nil
}

func buildWakeGoalRunPrompt(goal core.GeneratedGoal) string {
	return fmt.Sprintf("Execute this approved autonomous wake goal:\n\n%s\n\nConstraints:\n- Treat this as the single primary objective for the run.\n- Inspect the workspace before editing.\n- Keep changes scoped to this goal.\n- Run relevant verification before finishing.\n", strings.TrimSpace(goal.Content))
}

func shellQuoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}

func wakeStartCmd(cfgPath *string) *cobra.Command {
	var (
		intervalFlag string
		providerFlag string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the wake daemon",
		Long:  "Start the wake daemon which performs autonomous wake cycles on a recurring schedule.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !core.WakeOwnershipSupported() {
				return errors.New("wake daemon is currently supported on Linux hosts only")
			}
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			interval, err := resolveWakeInterval(cfg, cmd.Flags().Changed("interval"), intervalFlag)
			if err != nil {
				return err
			}
			statePath := core.DefaultWakeStatePath()
			provider := resolveWakeProvider(cfg, providerFlag)

			state, err := core.ReadWakeState(statePath)
			if err == nil && state.Status == core.WakeStatusRunning && core.WakeProcessOwned(state) {
				return fmt.Errorf("wake daemon already running (pid %d)", state.PID)
			}
			if err == nil && state.Status == core.WakeStatusRunning {
				if core.WakeProcessExists(state.PID) {
					return fmt.Errorf("wake state references live pid %d but ownership could not be verified", state.PID)
				}
				state.Status = core.WakeStatusStopped
				state.StoppedAt = ptrTime(time.Now())
				_ = core.WriteWakeState(statePath, state)
			}

			token, err := core.NewWakeToken()
			if err != nil {
				return err
			}

			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve current executable: %w", err)
			}
			exe, _ = filepath.EvalSymlinks(exe)

			var childArgs []string
			if strings.TrimSpace(*cfgPath) != "" {
				childArgs = append(childArgs, "--config", *cfgPath)
			}
			childArgs = append(childArgs,
				"wake", "run",
				"--state-path", statePath,
				"--interval", interval.String(),
				"--token", token,
			)
			if provider != "" {
				childArgs = append(childArgs, "--provider", provider)
			}

			logPath := core.DefaultWakeLogPath()
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				return fmt.Errorf("create wake log dir: %w", err)
			}
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return fmt.Errorf("open wake log: %w", err)
			}
			defer func() { _ = logFile.Close() }()

			child := exec.Command(exe, childArgs...)
			child.Stdout = logFile
			child.Stderr = logFile
			child.Stdin = nil
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := child.Start(); err != nil {
				return fmt.Errorf("start wake daemon: %w", err)
			}
			childPID := child.Process.Pid
			if err := waitForWakeReady(statePath, childPID, token, 5*time.Second); err != nil {
				_ = child.Process.Kill()
				return err
			}
			_ = child.Process.Release()

			fmt.Printf("✓ wake started\n")
			fmt.Printf("  pid: %d\n", childPID)
			fmt.Printf("  state: %s\n", statePath)
			fmt.Printf("  log: %s\n", logPath)
			fmt.Printf("  interval: %s\n", interval)
			if provider != "" {
				fmt.Printf("  provider: %s\n", provider)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&intervalFlag, "interval", "1h", "interval between ticks (e.g. 30m, 1h, 24h)")
	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider to use")

	return cmd
}

func wakeRunCmd(cfgPath *string) *cobra.Command {
	var (
		statePath    string
		intervalFlag string
		providerFlag string
		tokenFlag    string
	)

	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Run the wake daemon loop",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !core.WakeOwnershipSupported() {
				return errors.New("wake daemon is currently supported on Linux hosts only")
			}
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			if strings.TrimSpace(statePath) == "" {
				statePath = core.DefaultWakeStatePath()
			}
			if strings.TrimSpace(tokenFlag) == "" {
				return errors.New("wake run requires a non-empty ownership token")
			}
			interval, err := resolveWakeInterval(cfg, true, intervalFlag)
			if err != nil {
				return err
			}
			provider := resolveWakeProvider(cfg, providerFlag)
			exe, _ := os.Executable()
			exe, _ = filepath.EvalSymlinks(exe)

			state := core.InitWakeState()
			now := time.Now()
			state.Status = core.WakeStatusRunning
			state.StartedAt = ptrTime(now)
			state.PID = os.Getpid()
			state.Token = tokenFlag
			state.Executable = exe
			state.IntervalSeconds = int(interval.Seconds())
			state.Provider = provider
			state.NextRunAt = now.Add(interval)
			if err := core.WriteWakeState(statePath, state); err != nil {
				return fmt.Errorf("write wake state: %w", err)
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(sigCh)

			for {
				if added, err := refreshWakeGoalQueueFromScan(state, "."); err == nil {
					now = time.Now()
					state.LastScanAt = &now
					state.LastScanCandidates = added
				} else {
					fmt.Fprintf(os.Stderr, "warn: wake scan failed: %v\n", err)
				}
				var activeGoal *core.GeneratedGoal
				if len(state.QueuedGoals) > 0 {
					activeGoal = &state.QueuedGoals[0]
				}
				runID, goals, issue, cycleErr := runWakeCycle(context.Background(), cfg, strings.TrimSpace(*cfgPath), provider, activeGoal, state)
				now = time.Now()
				state.LastRunAt = &now
				state.LastRunID = runID
				if issue != nil {
					state.CurrentIssueNumber = issue.Number
					state.CurrentIssueTitle = issue.Title
				} else {
					state.CurrentIssueNumber = 0
					state.CurrentIssueTitle = ""
				}
				if cycleErr != nil {
					if activeGoal != nil {
						core.RecordWakeGoalFeedback(state, *activeGoal, runID, false, cycleErr.Error(), now)
					}
					state.ConsecutiveFailures++
					delay := core.WakeCycleDelay(state.IntervalSeconds, cfg.Wake.MaxBackoffSecs, state.ConsecutiveFailures)
					backoffUntil := now.Add(delay)
					state.BackoffUntil = &backoffUntil
					state.NextRunAt = backoffUntil
					state.Status = core.WakeStatusRunning
					if cfg.Wake.MaxFailures > 0 && state.ConsecutiveFailures >= cfg.Wake.MaxFailures {
						state.Status = core.WakeStatusFailed
						state.StoppedAt = &now
						if err := core.WriteWakeState(statePath, state); err != nil {
							return fmt.Errorf("write failed wake state: %w", err)
						}
						return fmt.Errorf("wake cycle failed %d times: %w", state.ConsecutiveFailures, cycleErr)
					}
				} else {
					if activeGoal != nil {
						core.RecordWakeGoalFeedback(state, *activeGoal, runID, true, "wake cycle completed", now)
					}
					state.ConsecutiveFailures = 0
					state.BackoffUntil = nil
					state.Status = core.WakeStatusRunning
					if activeGoal != nil && len(state.QueuedGoals) > 0 {
						state.QueuedGoals = state.QueuedGoals[1:]
					}
					if len(goals) > 0 {
						state.QueuedGoals = core.RankGeneratedGoals(append(state.QueuedGoals, dedupeWakeGoals(state.QueuedGoals, goals)...))
					}
					state.NextRunAt = now.Add(interval)
				}
				if err := core.WriteWakeState(statePath, state); err != nil {
					return fmt.Errorf("write wake state: %w", err)
				}

				delay := time.Until(state.NextRunAt)
				if delay < 0 {
					delay = 0
				}
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
					continue
				case <-sigCh:
					timer.Stop()
					stoppedAt := time.Now()
					state.Status = core.WakeStatusStopped
					state.StoppedAt = &stoppedAt
					if err := core.WriteWakeState(statePath, state); err != nil {
						return fmt.Errorf("write stopped wake state: %w", err)
					}
					return nil
				}
			}
		},
	}

	cmd.Flags().StringVar(&statePath, "state-path", core.DefaultWakeStatePath(), "path to wake state file")
	cmd.Flags().StringVar(&intervalFlag, "interval", "1h", "interval between ticks")
	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider to use")
	cmd.Flags().StringVar(&tokenFlag, "token", "", "ownership token for this wake daemon")
	_ = cmd.Flags().MarkHidden("state-path")
	_ = cmd.Flags().MarkHidden("token")
	return cmd
}

func wakeStatusCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show wake daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !core.WakeOwnershipSupported() {
				fmt.Println("wake  unsupported  (pid ownership checks require Linux)")
				return nil
			}
			statePath := core.DefaultWakeStatePath()

			state, err := core.ReadWakeState(statePath)
			if err != nil {
				fmt.Println("wake  idle  (not started)")
				return nil
			}
			if state.Status == core.WakeStatusRunning && !core.WakeProcessOwned(state) {
				fmt.Printf("wake  stale  pid=%d\n", state.PID)
				fmt.Println("  note: recorded daemon process is not running or does not match current wake state ownership")
				return nil
			}

			fmt.Printf("wake  %s", state.Status)
			if state.PID > 0 {
				fmt.Printf("  pid=%d", state.PID)
			}
			fmt.Println()

			if state.IntervalSeconds > 0 {
				fmt.Printf("  interval:    %ds (%s)\n", state.IntervalSeconds, time.Duration(state.IntervalSeconds)*time.Second)
			}

			if state.LastRunAt != nil {
				fmt.Printf("  last run:    %s (run %s)\n", state.LastRunAt.Format(time.DateTime), state.LastRunID)
			}

			if state.NextRunAt.After(time.Now()) {
				remaining := time.Until(state.NextRunAt).Round(time.Second)
				fmt.Printf("  next run:    %s (in %s)\n", state.NextRunAt.Format(time.DateTime), remaining)
			}

			if state.ConsecutiveFailures > 0 {
				fmt.Printf("  failures:    %d\n", state.ConsecutiveFailures)
			}
			if len(state.QueuedGoals) > 0 {
				fmt.Printf("  queued:      %d\n", len(state.QueuedGoals))
				fmt.Printf("  next goal:   %s\n", state.QueuedGoals[0].Content)
			}
			if state.LastScanAt != nil {
				fmt.Printf("  last scan:   %s (%d new candidates)\n", state.LastScanAt.Format(time.DateTime), state.LastScanCandidates)
			}
			if state.CurrentIssueNumber > 0 {
				fmt.Printf("  issue:       #%d %s\n", state.CurrentIssueNumber, state.CurrentIssueTitle)
			}

			if state.BackoffUntil != nil && state.BackoffUntil.After(time.Now()) {
				retryIn := time.Until(*state.BackoffUntil).Round(time.Second)
				fmt.Printf("  backoff:     retry in %s\n", retryIn)
			}

			return nil
		},
	}
}

func wakeStopCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the wake daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !core.WakeOwnershipSupported() {
				return errors.New("wake daemon is currently supported on Linux hosts only")
			}
			statePath := core.DefaultWakeStatePath()

			state, err := core.ReadWakeState(statePath)
			if err != nil {
				fmt.Println("✓ wake already stopped (no state file)")
				return nil
			}

			if state.PID > 0 {
				if core.WakeProcessOwned(state) {
					if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
						return fmt.Errorf("kill process: %w", err)
					}
				} else if core.WakeProcessExists(state.PID) {
					return fmt.Errorf("refusing to stop pid %d: wake state ownership could not be verified", state.PID)
				}
			}

			state.Status = core.WakeStatusStopped
			state.StoppedAt = ptrTime(time.Now())
			if err := core.WriteWakeState(statePath, state); err != nil {
				return fmt.Errorf("write state: %w", err)
			}

			fmt.Println("✓ wake stopped")
			return nil
		},
	}
}

func wakeTaskCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "task <name>",
		Short: "Run a named wake task manually (one-shot)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskName := args[0]
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			var task *config.WakeTask
			for i := range cfg.Wake.Tasks {
				if cfg.Wake.Tasks[i].Name == taskName {
					task = &cfg.Wake.Tasks[i]
					break
				}
			}
			if task == nil {
				return fmt.Errorf("task %q not found in config; available tasks: %s", taskName, wakeTaskNames(cfg))
			}

			workspace, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve workspace: %w", err)
			}
			providerName := resolveWakeProvider(cfg, "")
			prov, err := buildProvider(cfg, providerName)
			if err != nil {
				return fmt.Errorf("build provider: %w", err)
			}

			state := core.InitWakeState()
			_, _, err = runWakeSynthesisTask(context.Background(), cfg, workspace, providerName, prov, task, state)
			if err != nil {
				return fmt.Errorf("task %q failed: %w", taskName, err)
			}
			fmt.Printf("✓ task %q completed\n", taskName)
			return nil
		},
	}
}

func wakeTaskNames(cfg *config.Config) string {
	names := make([]string, len(cfg.Wake.Tasks))
	for i, t := range cfg.Wake.Tasks {
		names[i] = t.Name
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func resolveWakeInterval(cfg *config.Config, flagChanged bool, intervalFlag string) (time.Duration, error) {
	if !flagChanged && cfg != nil && cfg.Wake.IntervalSeconds > 0 {
		return time.Duration(cfg.Wake.IntervalSeconds) * time.Second, nil
	}
	interval, err := time.ParseDuration(intervalFlag)
	if err != nil {
		return 0, fmt.Errorf("invalid interval: %w", err)
	}
	if interval <= 0 {
		return 0, errors.New("interval must be > 0")
	}
	return interval, nil
}

func resolveWakeProvider(cfg *config.Config, providerFlag string) string {
	if strings.TrimSpace(providerFlag) != "" {
		return providerFlag
	}
	if cfg != nil && strings.TrimSpace(cfg.Wake.Provider) != "" {
		return cfg.Wake.Provider
	}
	if cfg != nil {
		return cfg.Defaults.Provider
	}
	return ""
}

func waitForWakeReady(statePath string, pid int, token string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := core.ReadWakeState(statePath)
		if err == nil && state.PID == pid && state.Token == token && state.Status == core.WakeStatusRunning {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("wake daemon did not initialize within %s", timeout)
}

func runWakeCycle(ctx context.Context, cfg *config.Config, cfgPath string, providerName string, activeGoal *core.GeneratedGoal, state *core.WakeState) (string, []core.GeneratedGoal, *wakeIssue, error) {
	workspace, err := os.Getwd()
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve workspace: %w", err)
	}

	if cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Wake.Mode), "issue_worker") {
		return runWakeIssueCycle(ctx, cfg, cfgPath, workspace, providerName, state)
	}

	prov, err := buildProvider(cfg, providerName)
	if err != nil {
		return "", nil, nil, fmt.Errorf("build provider %q: %w", providerName, err)
	}
	runID, goals, err := runWakeCycleWithProvider(ctx, cfg, workspace, providerName, prov, activeGoal)
	return runID, goals, nil, err
}

func runWakeCycleWithProvider(ctx context.Context, cfg *config.Config, workspace, providerName string, prov providers.Provider, activeGoal *core.GeneratedGoal) (string, []core.GeneratedGoal, error) {
	model := ""
	if pc, ok := cfg.Providers[providerName]; ok {
		model = pc.DefaultModel
	}
	if model == "" {
		if defaults := config.DefaultConfig(); defaults != nil {
			if pc, ok := defaults.Providers[providerName]; ok {
				model = pc.DefaultModel
			}
		}
	}

	runID := newRunID()
	runDir := filepath.Join("runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return runID, nil, fmt.Errorf("create wake run dir: %w", err)
	}

	meta := core.RunMeta{
		RunID:           runID,
		Name:            "wake-cycle",
		Provider:        prov.Name(),
		Model:           model,
		SourceWorkspace: workspace,
		CreatedAt:       time.Now().UTC(),
	}
	if err := core.WriteMeta(runDir, meta); err != nil {
		return runID, nil, fmt.Errorf("write wake meta: %w", err)
	}

	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		return runID, nil, fmt.Errorf("open wake trace: %w", err)
	}
	defer func() { _ = trace.Close() }()

	reg := tools.NewRegistry(nil)
	pol := policy.Default()

	loop := &core.Loop{
		Run: &core.Run{
			ID:        runID,
			Dir:       workspace,
			TraceFile: trace.Path(),
			Budget: core.Budget{
				MaxSteps:   cfg.Wake.BudgetSteps,
				MaxTokens:  cfg.Wake.BudgetTokens,
				MaxCostUSD: cfg.Wake.BudgetCostUSD,
			},
		},
		Provider:      prov,
		Tools:         reg,
		Policy:        pol,
		Trace:         trace,
		Budget:        core.NewBudgetTracker(&core.Budget{MaxSteps: cfg.Wake.BudgetSteps, MaxTokens: cfg.Wake.BudgetTokens, MaxCostUSD: cfg.Wake.BudgetCostUSD}),
		ConfirmFn:     func(_, _ string) bool { return false },
		Mapper:        core.NewPathMapper(workspace, workspace),
		NetworkTier:   "off",
		ModelMetadata: providersModelMetadata(ctx, prov, model),
	}

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:        pol.Name,
		Provider:      prov.Name(),
		Model:         model,
		Workspace:     workspace,
		ModelMetadata: loop.ModelMetadata,
	}); err != nil {
		return runID, nil, fmt.Errorf("emit wake run start: %w", err)
	}

	stepPrompt, err := buildWakePrompt(workspace, activeGoal)
	if err != nil {
		_ = loop.EmitRunEnd("error", "")
		return runID, nil, err
	}
	if err := loop.Step(ctx, stepPrompt); err != nil {
		_ = loop.EmitRunError("wake-cycle", err.Error())
		_ = loop.EmitRunEnd("error", "")
		return runID, nil, fmt.Errorf("wake step: %w", err)
	}

	goals := extractWakeGoals(loop.Messages)
	if len(goals) > 0 {
		meta.GeneratedGoals = append(meta.GeneratedGoals, goals...)
		if err := core.WriteMeta(runDir, meta); err != nil {
			_ = loop.EmitRunError("wake-cycle", err.Error())
			_ = loop.EmitRunEnd("error", "")
			return runID, nil, fmt.Errorf("persist wake goals: %w", err)
		}
		if err := emitWakeGeneratedGoals(trace, runID, "wake-cycle", goals); err != nil {
			_ = loop.EmitRunError("wake-cycle", err.Error())
			_ = loop.EmitRunEnd("error", "")
			return runID, nil, fmt.Errorf("emit wake goals: %w", err)
		}
	}

	if err := loop.EmitRunEnd("wake_cycle_complete", ""); err != nil {
		return runID, goals, fmt.Errorf("emit wake run end: %w", err)
	}
	return runID, goals, nil
}

func runWakeIssueCycle(ctx context.Context, cfg *config.Config, cfgPath, workspace, providerName string, state *core.WakeState) (string, []core.GeneratedGoal, *wakeIssue, error) {
	if cfg == nil {
		return "", nil, nil, errors.New("wake issue worker requires config")
	}
	if !cfg.Sandbox.Enabled {
		return "", nil, nil, errors.New("wake issue worker requires sandbox.enabled=true")
	}
	cleanBefore, err := wakeGitClean(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	if !cleanBefore {
		return "", nil, nil, errors.New("wake issue worker requires a clean working tree before starting a cycle")
	}
	currentBranch, err := wakeGitCurrentBranch(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	defaultBranch, err := wakeGitDefaultBranch(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	if currentBranch != defaultBranch {
		return "", nil, nil, fmt.Errorf("wake issue worker only auto-pushes and closes from the default branch (%s); current branch is %s", defaultBranch, currentBranch)
	}
	issue, err := selectWakeIssue(ctx, cfg, state)
	if err != nil {
		return "", nil, nil, err
	}
	if issue == nil {
		return "", nil, nil, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", nil, issue, fmt.Errorf("resolve current executable: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	headBefore, err := wakeGitHead(ctx)
	if err != nil {
		return "", nil, issue, err
	}
	prompt := buildWakeIssuePrompt(cfg, workspace, *issue)
	runID, err := runHeadlessIssueWorker(ctx, cfg, exe, cfgPath, prompt, providerName)
	if err != nil {
		return runID, nil, issue, err
	}
	headAfter, err := wakeGitHead(ctx)
	if err != nil {
		return runID, nil, issue, err
	}
	if headAfter == "" || headAfter == headBefore {
		return runID, nil, issue, fmt.Errorf("issue #%d run completed without creating a new commit", issue.Number)
	}
	commitCount, err := wakeGitCommitCount(ctx, headBefore, headAfter)
	if err != nil {
		return runID, nil, issue, err
	}
	if commitCount != 1 {
		return runID, nil, issue, fmt.Errorf("issue #%d run created %d commits; expected exactly 1", issue.Number, commitCount)
	}
	clean, err := wakeGitClean(ctx)
	if err != nil {
		return runID, nil, issue, err
	}
	if !clean {
		return runID, nil, issue, fmt.Errorf("issue #%d run left a dirty working tree", issue.Number)
	}
	if err := wakeGitPush(ctx, currentBranch); err != nil {
		return runID, nil, issue, err
	}

	closed, err := wakeIssueClosed(ctx, cfg, issue.Number)
	if err != nil {
		return runID, nil, issue, err
	}
	if !closed {
		if err := wakeIssueClose(ctx, cfg, issue.Number, headAfter); err != nil {
			return runID, nil, issue, err
		}
		closed, err = wakeIssueClosed(ctx, cfg, issue.Number)
		if err != nil {
			return runID, nil, issue, err
		}
	}
	if !closed {
		return runID, nil, issue, fmt.Errorf("issue #%d remains open after autonomous run", issue.Number)
	}
	return runID, nil, nil, nil
}

func providersModelMetadata(ctx context.Context, prov providers.Provider, model string) providers.ModelMetadata {
	if prov == nil {
		return providers.ModelMetadata{}
	}
	meta, err := prov.Metadata(ctx, model)
	if err != nil {
		return providers.ModelMetadata{}
	}
	return meta
}

func defaultWakeExecCommand(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func refreshWakeGoalQueueFromScan(state *core.WakeState, workspace string) (int, error) {
	if state == nil {
		return 0, nil
	}
	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		return 0, err
	}
	candidates = core.RankGoalCandidates(candidates)
	now := time.Now().UTC()
	incoming := make([]core.GeneratedGoal, 0, len(candidates))
	for _, candidate := range candidates {
		incoming = append(incoming, core.GeneratedGoal{
			ID:        fmt.Sprintf("scan-goal-%x", randBytes(4)),
			Content:   candidate.Content,
			StepID:    "wake-scan",
			CreatedAt: now,
		})
	}
	before := len(state.QueuedGoals)
	state.QueuedGoals = core.RankGeneratedGoals(append(state.QueuedGoals, dedupeWakeGoals(state.QueuedGoals, incoming)...))
	return len(state.QueuedGoals) - before, nil
}

func selectWakeIssue(ctx context.Context, cfg *config.Config, state *core.WakeState) (*wakeIssue, error) {
	if state != nil && state.CurrentIssueNumber > 0 {
		issue, err := wakeIssueView(ctx, cfg, state.CurrentIssueNumber)
		if err == nil && issue != nil && strings.EqualFold(issue.State, "OPEN") {
			return issue, nil
		}
	}

	issues, err := wakeIssueList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, nil
	}
	return &issues[0], nil
}

func wakeIssueList(ctx context.Context, cfg *config.Config) ([]wakeIssue, error) {
	limit := 20
	if cfg != nil && cfg.Wake.IssueLimit > 0 {
		limit = cfg.Wake.IssueLimit
	}
	args := []string{"issue", "list", "--state", "open", "--limit", strconv.Itoa(limit), "--json", "number,title,body,labels"}
	if repo := strings.TrimSpace(cfg.Wake.Repo); repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := wakeExecCommand(ctx, "", "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %w\n%s", err, strings.TrimSpace(out))
	}
	var issues []wakeIssue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("parse gh issue list: %w", err)
	}
	return issues, nil
}

func wakeIssueView(ctx context.Context, cfg *config.Config, number int) (*wakeIssue, error) {
	args := []string{"issue", "view", strconv.Itoa(number), "--json", "number,title,body,state,labels"}
	if repo := strings.TrimSpace(cfg.Wake.Repo); repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := wakeExecCommand(ctx, "", "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("gh issue view #%d: %w\n%s", number, err, strings.TrimSpace(out))
	}
	var issue wakeIssue
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		return nil, fmt.Errorf("parse gh issue view: %w", err)
	}
	return &issue, nil
}

func wakeIssueClosed(ctx context.Context, cfg *config.Config, number int) (bool, error) {
	issue, err := wakeIssueView(ctx, cfg, number)
	if err != nil {
		return false, err
	}
	return issue != nil && strings.EqualFold(issue.State, "CLOSED"), nil
}

func wakeIssueClose(ctx context.Context, cfg *config.Config, number int, commit string) error {
	args := []string{"issue", "close", strconv.Itoa(number), "--comment", fmt.Sprintf("Fixed in %s", commit)}
	if repo := strings.TrimSpace(cfg.Wake.Repo); repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := wakeExecCommand(ctx, "", "gh", args...)
	if err != nil {
		return fmt.Errorf("gh issue close #%d: %w\n%s", number, err, strings.TrimSpace(out))
	}
	return nil
}

func buildWakeIssuePrompt(cfg *config.Config, workspace string, issue wakeIssue) string {
	objective := strings.TrimSpace(cfg.Wake.Objective)
	if objective == "" {
		objective = "Look at GitHub open issues, pick one, implement it, test, lint, build, verify, review, commit, close the issue, and move on."
	}
	var labels []string
	for _, label := range issue.Labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			labels = append(labels, name)
		}
	}

	workspaceLabel := "current repository root"
	if base := filepath.Base(strings.TrimSpace(workspace)); base != "" && base != "." && base != string(filepath.Separator) {
		workspaceLabel = fmt.Sprintf("current repository root (%s)", base)
	}

	return fmt.Sprintf(
		"Autonomous daemon objective:\n%s\n\n"+
			"Repository workspace: %s\n"+
			"Selected GitHub issue: #%d %s\n"+
			"Labels: %s\n\n"+
			"Issue body:\n%s\n\n"+
			"Execution notes:\n"+
			"- You are already running from the repository root inside the sandbox workspace.\n"+
			"- Use relative paths like `cmd/v100/cmd_run.go` or `dogfood/verify_test.toml` with repo tools.\n"+
			"- Do not pass the absolute host workspace path to repo tools.\n\n"+
			"Required workflow:\n"+
			"1. Inspect the code and choose the minimal correct implementation.\n"+
			"2. Make the code changes.\n"+
			"3. Run exactly these verification commands if relevant:\n"+
			"   - ./scripts/lint.sh\n"+
			"   - env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...\n"+
			"   - env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go build ./...\n"+
			"4. Review your own diff for regressions and incomplete edge cases.\n"+
			"5. Commit with a focused message only if verification passes.\n"+
			"6. Do not push and do not close the GitHub issue yourself; the daemon will handle that after verifying your commit.\n"+
			"7. If blocked or verification fails, do not commit.\n"+
			"8. Work end-to-end in this run; do not stop after analysis.\n",
		objective,
		workspaceLabel,
		issue.Number,
		issue.Title,
		strings.Join(labels, ", "),
		strings.TrimSpace(issue.Body),
	)
}

func runHeadlessIssueWorker(ctx context.Context, cfg *config.Config, exe, cfgPath, prompt, providerName string) (string, error) {
	args := []string{}
	if cfgPath != "" {
		args = append(args, "--config", cfgPath)
	}
	args = append(args, "run", "--auto", "--unsafe", "--exit", "--sandbox", "--disable-watchdogs", "--provider", providerName)
	if cfg != nil && cfg.Wake.BudgetSteps > 0 {
		args = append(args, "--budget-steps", strconv.Itoa(cfg.Wake.BudgetSteps))
	}
	if cfg != nil && cfg.Wake.BudgetTokens > 0 {
		args = append(args, "--budget-tokens", strconv.Itoa(cfg.Wake.BudgetTokens))
	}
	maxToolCalls := 0
	if cfg != nil {
		maxToolCalls = cfg.Defaults.MaxToolCallsPerStep
		if pol, ok := cfg.Policies["default"]; ok && pol.MaxToolCallsPerStep > maxToolCalls {
			maxToolCalls = pol.MaxToolCallsPerStep
		}
	}
	if maxToolCalls <= 0 {
		maxToolCalls = 50
	}
	args = append(args, "--max-tool-calls-per-step", strconv.Itoa(maxToolCalls), "--prompt-file", "-")
	out, err := wakeExecCommand(ctx, prompt, exe, args...)
	runID := extractRunIDFromOutput(out)
	if err != nil {
		if runID == "" {
			return "", fmt.Errorf("wake issue worker run: %w\n%s", err, strings.TrimSpace(out))
		}
		return runID, fmt.Errorf("wake issue worker run: %w\n%s", err, strings.TrimSpace(out))
	}
	return runID, nil
}

func wakeGitHead(ctx context.Context) (string, error) {
	out, err := wakeExecCommand(ctx, "", "git", "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w\n%s", err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

func wakeGitClean(ctx context.Context) (bool, error) {
	out, err := wakeExecCommand(ctx, "", "git", "status", "--short")
	if err != nil {
		return false, fmt.Errorf("git status --short: %w\n%s", err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out) == "", nil
}

func wakeGitCurrentBranch(ctx context.Context) (string, error) {
	out, err := wakeExecCommand(ctx, "", "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --abbrev-ref HEAD: %w\n%s", err, strings.TrimSpace(out))
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("wake issue worker requires a named local branch")
	}
	return branch, nil
}

func wakeGitDefaultBranch(ctx context.Context) (string, error) {
	out, err := wakeExecCommand(ctx, "", "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("git symbolic-ref refs/remotes/origin/HEAD: %w\n%s", err, strings.TrimSpace(out))
	}
	ref := strings.TrimSpace(out)
	if ref == "" {
		return "", errors.New("could not determine origin default branch")
	}
	return strings.TrimPrefix(ref, "origin/"), nil
}

func wakeGitCommitCount(ctx context.Context, from, to string) (int, error) {
	out, err := wakeExecCommand(ctx, "", "git", "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s..%s: %w\n%s", from, to, err, strings.TrimSpace(out))
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return 0, fmt.Errorf("parse git rev-list count %q: %w", strings.TrimSpace(out), convErr)
	}
	return count, nil
}

func wakeGitPush(ctx context.Context, branch string) error {
	out, err := wakeExecCommand(ctx, "", "git", "push", "origin", "HEAD:"+branch)
	if err != nil {
		return fmt.Errorf("git push origin HEAD:%s: %w\n%s", branch, err, strings.TrimSpace(out))
	}
	return nil
}

func extractRunIDFromOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "run id:") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "run id:"))
	}
	return ""
}

func buildWakePrompt(workspace string, activeGoal *core.GeneratedGoal) (string, error) {
	summary, err := collectWakeWorkspaceSummary(workspace, 2, 40)
	if err != nil {
		return "", fmt.Errorf("scan workspace: %w", err)
	}
	candidates, err := core.ScanWorkspaceGoalCandidates(workspace)
	if err != nil {
		return "", fmt.Errorf("scan workspace goal candidates: %w", err)
	}
	candidates = core.RankGoalCandidates(candidates)
	candidateSection := formatWakeCandidateGoals(candidates)
	if activeGoal != nil && strings.TrimSpace(activeGoal.Content) != "" {
		return fmt.Sprintf(
			"You are running an autonomous wake cycle for this workspace.\n"+
				"Workspace: %s\n"+
				"Observed workspace summary:\n%s\n\n"+
				"%s"+
				"Prior queued goal:\n%s\n\n"+
				"Refine that queued goal into the single best immediate next-step engineering goal.\n"+
				"Constraints:\n"+
				"- Do not use tools.\n"+
				"- Prefer a local candidate goal when it is clearly better grounded than the queued goal.\n"+
				"- Keep continuity with the queued goal, but make the result more concrete and immediately actionable.\n"+
				"- Respond using exactly this format:\n"+
				"  GOAL: <one sentence>\n"+
				"  WHY: <one sentence>\n"+
				"- If the queued goal is no longer useful, replace it with a better one.\n"+
				"- If no meaningful goal is evident, respond exactly:\n"+
				"  GOAL: No actionable wake goal.\n"+
				"  WHY: Workspace signals are currently too weak.\n",
			workspace,
			summary,
			candidateSection,
			activeGoal.Content,
		), nil
	}

	return fmt.Sprintf(
		"You are running an autonomous wake cycle for this workspace.\n"+
			"Workspace: %s\n"+
			"Observed workspace summary:\n%s\n\n"+
			"%s"+
			"Produce exactly one concrete next-step engineering goal that would materially improve or advance this workspace.\n"+
			"Constraints:\n"+
			"- Do not use tools.\n"+
			"- Ground the goal in the candidate list when one is already concrete enough.\n"+
			"- Respond using exactly this format:\n"+
			"  GOAL: <one sentence>\n"+
			"  WHY: <one sentence>\n"+
			"- The goal must be specific and actionable.\n"+
			"- If no meaningful goal is evident, respond exactly:\n"+
			"  GOAL: No actionable wake goal.\n"+
			"  WHY: Workspace signals are currently too weak.\n",
		workspace,
		summary,
		candidateSection,
	), nil
}

func formatWakeCandidateGoals(candidates []core.GoalCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Candidate goals from local signals:\n")
	for _, candidate := range candidates {
		b.WriteString("- ")
		b.WriteString(candidate.Content)
		b.WriteString("\n  source: ")
		b.WriteString(candidate.SourceAttribution)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func extractWakeGoals(messages []providers.Message) []core.GeneratedGoal {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			return nil
		}
		goal := parseWakeGoal(content)
		if goal == "" || strings.EqualFold(goal, "No actionable wake goal.") {
			return nil
		}
		return core.RankGeneratedGoals([]core.GeneratedGoal{{
			ID:        fmt.Sprintf("wake-goal-%x", randBytes(4)),
			Content:   goal,
			StepID:    "wake-cycle",
			CreatedAt: time.Now().UTC(),
		}})
	}
	return nil
}

func collectWakeWorkspaceSummary(workspace string, maxDepth, maxEntries int) (string, error) {
	var entries []string
	err := filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if shouldSkipWakePath(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Count(rel, "/") >= maxDepth && d.IsDir() {
			entries = append(entries, rel+"/")
			if len(entries) >= maxEntries {
				return fs.SkipAll
			}
			return filepath.SkipDir
		}
		if d.IsDir() {
			entries = append(entries, rel+"/")
		} else {
			entries = append(entries, rel)
		}
		if len(entries) >= maxEntries {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return "", err
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return "(no visible files)", nil
	}
	return "- " + strings.Join(entries, "\n- "), nil
}

func shouldSkipWakePath(rel string, d fs.DirEntry) bool {
	top := strings.Split(rel, "/")[0]
	if top == "runs" || top == ".git" {
		return true
	}
	if strings.HasPrefix(top, ".gocache") || strings.HasPrefix(top, ".gomodcache") {
		return true
	}
	return false
}

func parseWakeGoal(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "GOAL:") {
			continue
		}
		goal := strings.TrimSpace(line[len("GOAL:"):])
		goal = strings.TrimLeft(goal, "-*0123456789. ")
		return strings.TrimSpace(goal)
	}
	line := strings.TrimSpace(strings.Split(content, "\n")[0])
	line = strings.TrimLeft(line, "-*0123456789. ")
	return strings.TrimSpace(line)
}

func emitWakeGeneratedGoals(trace *core.TraceWriter, runID, stepID string, goals []core.GeneratedGoal) error {
	for _, goal := range goals {
		payload := core.GeneratedGoalPayload{
			Content: goal.Content,
			StepID:  goal.StepID,
			Score:   goal.Score,
			Reasons: goal.Reasons,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := trace.Write(core.Event{
			TS:      time.Now().UTC(),
			RunID:   runID,
			StepID:  stepID,
			EventID: fmt.Sprintf("wake-goal-%x", randBytes(4)),
			Type:    core.EventGeneratedGoal,
			Payload: b,
		}); err != nil {
			return err
		}
	}
	return nil
}

func dedupeWakeGoals(existing, incoming []core.GeneratedGoal) []core.GeneratedGoal {
	seen := make(map[string]struct{}, len(existing))
	for _, goal := range existing {
		key := strings.ToLower(strings.TrimSpace(goal.Content))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	out := make([]core.GeneratedGoal, 0, len(incoming))
	for _, goal := range incoming {
		goal = core.ScoreGeneratedGoal(goal)
		key := strings.ToLower(strings.TrimSpace(goal.Content))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, goal)
	}
	return out
}

func runWakeSynthesisTask(ctx context.Context, cfg *config.Config, workspace, providerName string, prov providers.Provider, task *config.WakeTask, state *core.WakeState) (string, []core.GeneratedGoal, error) {
	model := resolveWakeModel(cfg, providerName)

	runID := newRunID()
	runDir := filepath.Join("runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return runID, nil, fmt.Errorf("create wake run dir: %w", err)
	}

	meta := core.RunMeta{
		RunID:           runID,
		Name:            "wake-synthesis-" + task.Name,
		Provider:        prov.Name(),
		Model:           model,
		SourceWorkspace: workspace,
		CreatedAt:       time.Now().UTC(),
	}
	if err := core.WriteMeta(runDir, meta); err != nil {
		return runID, nil, fmt.Errorf("write wake meta: %w", err)
	}

	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		return runID, nil, fmt.Errorf("open wake trace: %w", err)
	}
	defer func() { _ = trace.Close() }()

	var carryContext string

	for i, step := range task.Steps {
		reg := buildScopedToolRegistry(cfg, step.EnabledTools()...)

		pol := policy.Default()
		loop := &core.Loop{
			Run: &core.Run{
				ID:        runID,
				Dir:       workspace,
				TraceFile: trace.Path(),
				Budget: core.Budget{
					MaxSteps:   10,
					MaxTokens:  cfg.Wake.BudgetTokens / len(task.Steps),
					MaxCostUSD: cfg.Wake.BudgetCostUSD / float64(len(task.Steps)),
				},
			},
			Provider:      prov,
			Tools:         reg,
			Policy:        pol,
			Trace:         trace,
			Budget:        core.NewBudgetTracker(&core.Budget{MaxSteps: 10, MaxTokens: cfg.Wake.BudgetTokens / len(task.Steps), MaxCostUSD: cfg.Wake.BudgetCostUSD / float64(len(task.Steps))}),
			ConfirmFn:     func(_, _ string) bool { return true },
			Mapper:        core.NewPathMapper(workspace, workspace),
			NetworkTier:   "open",
			ModelMetadata: providersModelMetadata(ctx, prov, model),
		}

		stepPrompt := buildStepPrompt(task, i, step, carryContext)

		if err := loop.Step(ctx, stepPrompt); err != nil {
			_ = loop.EmitRunError("wake-synthesis", err.Error())
			return runID, nil, fmt.Errorf("step %d (%s): %w", i+1, step.Name, err)
		}

		carryContext = lastAssistantMessage(loop.Messages)
	}

	if carryContext != "" {
		output := carryContext
		if idx := strings.Index(strings.ToUpper(output), "SYNTHESIS:"); idx >= 0 {
			output = strings.TrimSpace(output[idx+len("SYNTHESIS:"):])
		}
		outputPath := filepath.Join(runDir, "synthesis.txt")
		if err := os.WriteFile(outputPath, []byte(output), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to write synthesis output: %v\n", err)
		}
	}

	_ = core.WriteMeta(runDir, meta)
	return runID, nil, nil
}

func buildScopedToolRegistry(cfg *config.Config, toolNames ...string) *tools.Registry {
	if len(toolNames) == 0 {
		return tools.NewRegistry(nil)
	}

	reg := tools.NewRegistry(toolNames)
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.FSList())
	reg.Register(tools.FSMkdir())
	reg.Register(tools.FSRenderImage())
	reg.Register(tools.Sh())
	reg.Register(tools.CurlFetch())
	reg.Register(tools.WebExtract())
	reg.Register(tools.WebSearch())
	reg.Register(tools.NewsFetch())
	reg.Register(tools.Wiki())
	reg.Register(tools.ProjectSearch())
	reg.Register(tools.ProvenanceLookup())
	reg.Register(tools.ATProtoFeed(cfg))
	reg.Register(tools.ATProtoNotifications(cfg))
	reg.Register(tools.ATProtoPost(cfg))
	reg.Register(tools.ATProtoCreateRecord(cfg))
	reg.Register(tools.ATProtoResolve(cfg))
	reg.Register(tools.ATProtoGetFollows(cfg))
	reg.Register(tools.ATProtoGetFollowers(cfg))
	reg.Register(tools.ATProtoGetProfile(cfg))
	reg.Register(tools.ATProtoFollowerMomentum(cfg))
	reg.Register(tools.ATProtoGraphExplorer(cfg))
	reg.Register(tools.ATProtoCommunityDetect(cfg))
	reg.Register(tools.ATProtoEngagementHealth(cfg))
	reg.Register(tools.ATProtoVibeCheck(cfg))
	reg.Register(tools.ATProtoDailyDigest(cfg))
	reg.Register(tools.ATProtoIndex(cfg))
	reg.Register(tools.ATProtoRecall(cfg))
	reg.Register(tools.ATProtoAnonSynth(cfg))
	reg.Register(tools.BlackboardRead())
	reg.Register(tools.BlackboardWrite())
	return reg
}

func buildStepPrompt(task *config.WakeTask, stepIndex int, step config.WakeTaskStep, carryContext string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Task: %s — Step %d of %d: %s\n", task.Name, stepIndex+1, len(task.Steps), step.Name)
	fmt.Fprintf(&b, "%s\n\n", step.Prompt)

	if enabled := step.EnabledTools(); len(enabled) > 0 {
		fmt.Fprintf(&b, "Available tools: %s. Use them as needed.\n", strings.Join(enabled, ", "))
	}

	if carryContext != "" {
		b.WriteString("\nContext from previous step:\n")
		b.WriteString(carryContext)
		b.WriteString("\n")
	}

	if stepIndex == len(task.Steps)-1 {
		b.WriteString("\nEnd your response with:\nSYNTHESIS:\n<your final output>\n")
	}

	return b.String()
}

func resolveWakeModel(cfg *config.Config, providerName string) string {
	if pc, ok := cfg.Providers[providerName]; ok {
		return pc.DefaultModel
	}
	if defaults := config.DefaultConfig(); defaults != nil {
		if pc, ok := defaults.Providers[providerName]; ok {
			return pc.DefaultModel
		}
	}
	return ""
}

func lastAssistantMessage(messages []providers.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

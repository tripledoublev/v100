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
	)

	return cmd
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
			if err := waitForWakeReady(statePath, child.Process.Pid, token, 5*time.Second); err != nil {
				_ = child.Process.Kill()
				return err
			}
			_ = child.Process.Release()

			fmt.Printf("✓ wake started\n")
			fmt.Printf("  pid: %d\n", child.Process.Pid)
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
					state.ConsecutiveFailures = 0
					state.BackoffUntil = nil
					state.Status = core.WakeStatusRunning
					if activeGoal != nil && len(state.QueuedGoals) > 0 {
						state.QueuedGoals = state.QueuedGoals[1:]
					}
					if len(goals) > 0 {
						state.QueuedGoals = append(state.QueuedGoals, dedupeWakeGoals(state.QueuedGoals, goals)...)
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
	pol.MemoryPath = filepath.Join(workspace, "MEMORY.md")

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
	prompt := buildWakeIssuePrompt(cfg, workspace, *issue)
	runID, err := runHeadlessIssueWorker(ctx, exe, cfgPath, prompt, providerName)
	if err != nil {
		return runID, nil, issue, err
	}

	closed, err := wakeIssueClosed(ctx, cfg, issue.Number)
	if err != nil {
		return runID, nil, issue, err
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

	return fmt.Sprintf(
		"Autonomous daemon objective:\n%s\n\n"+
			"Repository workspace: %s\n"+
			"Selected GitHub issue: #%d %s\n"+
			"Labels: %s\n\n"+
			"Issue body:\n%s\n\n"+
			"Required workflow:\n"+
			"1. Inspect the code and choose the minimal correct implementation.\n"+
			"2. Make the code changes.\n"+
			"3. Run exactly these verification commands if relevant:\n"+
			"   - ./scripts/lint.sh\n"+
			"   - env GOCACHE=%s/.gocache go test ./...\n"+
			"   - env GOCACHE=%s/.gocache go build ./...\n"+
			"4. Review your own diff for regressions and incomplete edge cases.\n"+
			"5. Commit with a focused message only if verification passes.\n"+
			"6. Close GitHub issue #%d only after the commit succeeds.\n"+
			"7. If blocked or verification fails, do not commit and do not close the issue.\n"+
			"8. Work end-to-end in this run; do not stop after analysis.\n",
		objective,
		workspace,
		issue.Number,
		issue.Title,
		strings.Join(labels, ", "),
		strings.TrimSpace(issue.Body),
		workspace,
		workspace,
		issue.Number,
	)
}

func runHeadlessIssueWorker(ctx context.Context, exe, cfgPath, prompt, providerName string) (string, error) {
	args := []string{}
	if cfgPath != "" {
		args = append(args, "--config", cfgPath)
	}
	args = append(args, "run", "--auto", "--unsafe", "--exit", "--sandbox", "--provider", providerName, "--prompt-file", "-")
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
	if activeGoal != nil && strings.TrimSpace(activeGoal.Content) != "" {
		return fmt.Sprintf(
			"You are running an autonomous wake cycle for this workspace.\n"+
				"Workspace: %s\n"+
				"Observed workspace summary:\n%s\n\n"+
				"Prior queued goal:\n%s\n\n"+
				"Refine that queued goal into the single best immediate next-step engineering goal.\n"+
				"Constraints:\n"+
				"- Do not use tools.\n"+
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
			activeGoal.Content,
		), nil
	}

	return fmt.Sprintf(
		"You are running an autonomous wake cycle for this workspace.\n"+
			"Workspace: %s\n"+
			"Observed workspace summary:\n%s\n\n"+
			"Produce exactly one concrete next-step engineering goal that would materially improve or advance this workspace.\n"+
			"Constraints:\n"+
			"- Do not use tools.\n"+
			"- Respond using exactly this format:\n"+
			"  GOAL: <one sentence>\n"+
			"  WHY: <one sentence>\n"+
			"- The goal must be specific and actionable.\n"+
			"- If no meaningful goal is evident, respond exactly:\n"+
			"  GOAL: No actionable wake goal.\n"+
			"  WHY: Workspace signals are currently too weak.\n",
		workspace,
		summary,
	), nil
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
		return []core.GeneratedGoal{{
			ID:        fmt.Sprintf("wake-goal-%x", randBytes(4)),
			Content:   goal,
			StepID:    "wake-cycle",
			CreatedAt: time.Now().UTC(),
		}}
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

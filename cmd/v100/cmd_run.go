package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func runCmd(cfgPath *string) *cobra.Command {
	var (
		providerFlag     string
		modelFlag        string
		policyFlag       string
		runDirFlag       string
		workspaceFlag    string
		unsafeFlag       bool
		autoFlag         bool
		sandboxFlag      bool
		streamingFlag    bool
		budgetStepsFlag  int
		budgetTokensFlag int
		budgetCostFlag   float64
		toolTimeoutFlag  int
		maxToolCallsFlag int
		confirmToolsFlag string
		maxReplansFlag   int
		tuiFlag          bool
		tuiNoAltFlag     bool
		tuiPlainFlag     bool
		tuiDebugFlag     bool
		nameFlag         string
		tagFlags         []string
		solverFlag       string
		authFlag         string
		baseURLFlag      string
		temperatureFlag  float64
		topPFlag         float64
		topKFlag         int
		maxTokensFlag    int
		seedFlag         int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start an interactive agent run",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			// Merge flags into config
			if providerFlag != "" {
				cfg.Defaults.Provider = providerFlag
			}
			if budgetStepsFlag > 0 {
				cfg.Defaults.BudgetSteps = budgetStepsFlag
			}
			if budgetTokensFlag > 0 {
				cfg.Defaults.BudgetTokens = budgetTokensFlag
			}
			if budgetCostFlag > 0 {
				cfg.Defaults.BudgetCostUSD = budgetCostFlag
			}
			if toolTimeoutFlag > 0 {
				cfg.Defaults.ToolTimeoutMS = toolTimeoutFlag
			}
			if maxToolCallsFlag > 0 {
				cfg.Defaults.MaxToolCallsPerStep = maxToolCallsFlag
			}
			if confirmToolsFlag != "" {
				cfg.Defaults.ConfirmTools = confirmToolsFlag
			}
			if solverFlag != "" {
				cfg.Defaults.Solver = solverFlag
			}
			if authFlag != "" {
				parts := strings.SplitN(authFlag, ":", 2)
				if len(parts) == 2 {
					if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
						pc.Auth.Username = parts[0]
						pc.Auth.Password = parts[1]
						cfg.Providers[cfg.Defaults.Provider] = pc
					}
				}
			}
			if baseURLFlag != "" {
				if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
					pc.BaseURL = baseURLFlag
					cfg.Providers[cfg.Defaults.Provider] = pc
				}
			}
			if cmd.Flags().Changed("sandbox") {
				cfg.Sandbox.Enabled = sandboxFlag
			}
			if maxReplansFlag > 0 {
				cfg.Defaults.MaxReplans = maxReplansFlag
			}

			// Build run directory
			runID := newRunID()
			runBase := runDirFlag
			if runBase == "" {
				runBase = "runs"
			}
			runDir := filepath.Join(runBase, runID)
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				return fmt.Errorf("create run dir: %w", err)
			}
			_ = os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755)
			blackboardPath := filepath.Join(runDir, "blackboard.md")
			if _, err := os.Stat(blackboardPath); os.IsNotExist(err) {
				_ = os.WriteFile(blackboardPath, []byte("# Blackboard\n\n"), 0o644)
			}

			// Set workspace grounding
			sourceWorkspace := resolveWorkspace(workspaceFlag, runDir)

			// Write meta.json
			tags := parseTags(tagFlags)

			// Build provider first to get name and capabilities
			prov, err := buildProvider(cfg, cfg.Defaults.Provider)
			if err != nil {
				return err
			}

			// Decide model
			model := modelFlag
			if model == "" {
				if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
					model = pc.DefaultModel
				}
			}

			meta := core.RunMeta{
				RunID:           runID,
				Name:            nameFlag,
				Tags:            tags,
				Provider:        prov.Name(),
				Model:           model,
				SourceWorkspace: sourceWorkspace,
				CreatedAt:       time.Now().UTC(),
			}
			_ = core.WriteMeta(runDir, meta)

			tracePath := filepath.Join(runDir, "trace.jsonl")
			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return err
			}
			defer func() { _ = trace.Close() }()

			run := &core.Run{
				ID:        runID,
				Dir:       runDir,
				TraceFile: tracePath,
				Budget: core.Budget{
					MaxSteps:   cfg.Defaults.BudgetSteps,
					MaxTokens:  cfg.Defaults.BudgetTokens,
					MaxCostUSD: cfg.Defaults.BudgetCostUSD,
				},
			}

			// Build sandbox session
			session, mapper, workspace, err := buildSandboxSession(cfg, runID, sourceWorkspace, runBase)
			if err != nil {
				return err
			}
			if cfg.Sandbox.Enabled {
				defer func() { _ = session.Close() }()
			}

			// Build tool registry
			reg := buildToolRegistry(cfg)

			// Load policy
			pol := loadPolicy(cfg, policyFlag)
			if toolTimeoutFlag > 0 {
				pol.ToolTimeoutMS = toolTimeoutFlag
			}
			pol.MemoryPath = filepath.Join(workspace, "MEMORY.md")
			if cfg.Defaults.ContextLimit > 0 {
				pol.ContextLimit = cfg.Defaults.ContextLimit
			}
			if cmd.Flags().Changed("streaming") {
				pol.Streaming = streamingFlag
			}

			// Budget tracker
			budget := core.NewBudgetTracker(&run.Budget)

			// Decide confirm mode
			confirmMode := cfg.Defaults.ConfirmTools
			if autoFlag {
				confirmMode = "never"
			}
			if unsafeFlag {
				confirmMode = "never"
			}

			// Build generation params from flags and config defaults
			genParams := buildGenParams(cfg, temperatureFlag, topPFlag, topKFlag, maxTokensFlag, seedFlag, cmd)

			// Build solver
			solver, err := buildSolver(cfg, solverFlag)
			if err != nil {
				return err
			}

			if tuiFlag {
				return runWithTUI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, !tuiNoAltFlag, tuiPlainFlag, tuiDebugFlag, genParams, solver, strings.Join(args, " "), session, mapper)
			}
			return runWithCLI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, genParams, solver, strings.Join(args, " "), session, mapper)
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider name (overrides config)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model name (overrides config)")
	cmd.Flags().StringVar(&policyFlag, "policy", "default", "policy name")
	cmd.Flags().StringVar(&runDirFlag, "run-dir", "", "base directory for runs (default: ./runs)")
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "workspace directory for tool operations")
	cmd.Flags().BoolVar(&unsafeFlag, "unsafe", false, "disable path guardrails and confirmations")
	cmd.Flags().BoolVar(&autoFlag, "auto", false, "auto-approve all tool calls (no confirmation)")
	cmd.Flags().BoolVar(&sandboxFlag, "sandbox", false, "enable isolated sandbox execution")
	cmd.Flags().BoolVar(&streamingFlag, "streaming", false, "enable real-time token streaming")
	cmd.Flags().IntVar(&budgetStepsFlag, "budget-steps", 0, "max steps (0=config default)")
	cmd.Flags().IntVar(&budgetTokensFlag, "budget-tokens", 0, "max tokens (0=config default)")
	cmd.Flags().Float64Var(&budgetCostFlag, "budget-cost", 0, "max cost in USD (0=config default)")
	cmd.Flags().IntVar(&toolTimeoutFlag, "tool-timeout", 0, "tool timeout in ms (0=config default)")
	cmd.Flags().IntVar(&maxToolCallsFlag, "max-tool-calls-per-step", 0, "max tool calls per step")
	cmd.Flags().StringVar(&confirmToolsFlag, "confirm-tools", "", "confirm mode: always|dangerous|never")
	cmd.Flags().BoolVar(&tuiFlag, "tui", false, "enable Bubble Tea TUI")
	cmd.Flags().BoolVar(&tuiNoAltFlag, "tui-no-alt", false, "disable alternate screen mode in TUI (for terminal compatibility)")
	cmd.Flags().BoolVar(&tuiPlainFlag, "tui-plain", false, "force plain monochrome TUI rendering for terminal compatibility")
	cmd.Flags().BoolVar(&tuiDebugFlag, "tui-debug", false, "write TUI startup/runtime debug log to run directory")
	cmd.Flags().StringVar(&nameFlag, "name", "", "human-readable run name (stored in meta.json)")
	cmd.Flags().StringSliceVar(&tagFlags, "tag", nil, "key=value tags for the run (repeatable)")
	cmd.Flags().StringVar(&solverFlag, "solver", "", "solver type: react|plan_execute|router (default: react)")
	cmd.Flags().StringVar(&authFlag, "auth", "", "basic auth credentials (user:password) for providers like ollama")
	cmd.Flags().StringVar(&baseURLFlag, "base-url", "", "override the provider's base API URL")
	cmd.Flags().IntVar(&maxReplansFlag, "max-replans", 0, "max replans for plan_execute solver")
	cmd.Flags().Float64Var(&temperatureFlag, "temperature", 0, "sampling temperature (0=provider default)")
	cmd.Flags().Float64Var(&topPFlag, "top-p", 0, "nucleus sampling top-p (0=provider default)")
	cmd.Flags().IntVar(&topKFlag, "top-k", 0, "top-k sampling (0=provider default)")
	cmd.Flags().IntVar(&maxTokensFlag, "max-tokens", 0, "max output tokens (0=provider default)")
	cmd.Flags().IntVar(&seedFlag, "seed", 0, "random seed for reproducibility (0=none)")

	return cmd
}

func runWithCLI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, genParams providers.GenParams, solver core.Solver, initialPrompt string, session executor.Session, mapper *core.PathMapper) error {

	renderer := ui.NewCLIRenderer()

	confirmFn := buildConfirmFn(confirmMode)

	outputFn := core.OutputFn(renderer.RenderEvent)
	registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, workspace, pol.MaxToolCallsPerStep, session, mapper)

	loop := &core.Loop{
		Run:         run,
		Provider:    prov,
		Tools:       reg,
		Policy:      pol,
		Trace:       trace,
		Budget:      budget,
		ConfirmFn:   confirmFn,
		OutputFn:    outputFn,
		GenParams:   genParams,
		Solver:      solver,
		Session:     session,
		Mapper:      mapper,
		NetworkTier: loopNetworkTier(cfg),
		Snapshots:   buildSnapshotManager(cfg, workspace),
	}

	// Override workspace for tool execution
	run.Dir = workspace

	tracedWorkspace := workspace
	if cfg.Sandbox.Enabled {
		tracedWorkspace = "/workspace"
	}

	ctx := context.Background()
	metadata, _ := prov.Metadata(ctx, model)
	loop.ModelMetadata = metadata
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

		if err := loop.EmitRunStart(core.RunStartPayload{
			Policy:        pol.Name,
			Provider:      prov.Name(),
			Model:         model,
		Workspace:     tracedWorkspace,
		ModelMetadata: metadata,
	}); err != nil {
		return err
	}

	fmt.Println(ui.Info(ui.Dim("trace: ") + run.TraceFile))
	fmt.Println(ui.Info(ui.Dim("workspace: ") + workspace))
	fmt.Println(ui.Info(ui.Dim("budget: ") + budget.Summary()))
	fmt.Println(ui.Dim("Ctrl+C or /quit to exit"))

	reason := "user_exit"

	if initialPrompt != "" {
		if err := loop.Step(ctx, initialPrompt); err != nil {
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				fmt.Fprintln(os.Stderr, ui.Warn("budget exceeded: "+budgetErr.Reason))
				reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
				_ = loop.EmitRunEnd(reason)
				return nil
			}
			fmt.Fprintln(os.Stderr, ui.Fail("initial step error: "+err.Error()))
		}
	}

	for {
		input, err := ui.Prompt("")
		if err != nil {
			reason = "user_exit"
			break
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "/quit" || input == "/exit" {
			break
		}

		if err := loop.Step(ctx, input); err != nil {
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				fmt.Fprintln(os.Stderr, ui.Warn("budget exceeded: "+budgetErr.Reason))
				reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
				break
			}
			fmt.Fprintln(os.Stderr, ui.Fail("step error: "+err.Error()))
		}
	}

	_ = loop.EmitRunEnd(reason)

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		fmt.Fprintln(os.Stderr, ui.Warn("sandbox finalize: "+err.Error()))
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}

	fmt.Println(ui.Dim("budget: " + budget.Summary()))
	return nil
}

func runWithTUI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, useAltScreen bool, plainTTY bool, debug bool, genParams providers.GenParams, solver core.Solver, initialPrompt string, session executor.Session, mapper *core.PathMapper) error {

	run.Dir = workspace

	var logger *log.Logger
	if debug {
		logPath := filepath.Join(filepath.Dir(run.TraceFile), "tui.debug.log")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer func() { _ = f.Close() }()
			logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
			logger.Printf("start run_id=%s provider=%s model=%s alt=%t plain=%t", run.ID, prov.Name(), model, useAltScreen, plainTTY)
		}
	}

	var tui *ui.TUI
	ctx := context.Background()
	reason := "user_exit"

	var loop *core.Loop

	submitFn := func(input string) {
		if logger != nil {
			logger.Printf("submit input_len=%d", len(input))
		}
		inputTrim := strings.TrimSpace(input)
		if inputTrim == "/quit" || inputTrim == "/exit" {
			reason = "user_exit"
			tui.Quit()
			return
		}
		if err := loop.Step(ctx, input); err != nil {
			if logger != nil {
				logger.Printf("step error: %v", err)
			}
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				_ = loop.EmitRunEnd("budget_exceeded")
				tui.Quit()
			}
		}
	}

	tui = ui.NewTUI(submitFn, useAltScreen, plainTTY)

	confirmFn := func(toolName, args string) bool {
		if confirmMode == "never" {
			return true
		}
		if confirmMode == "always" || (confirmMode == "dangerous" && reg.IsDangerous(toolName)) {
			return tui.RequestConfirm(toolName, args)
		}
		return true
	}

	tuiOutputFn := core.OutputFn(func(ev core.Event) { tui.SendEvent(ev) })
	registerAgentTool(cfg, reg, trace, budget, &tuiOutputFn, confirmFn, workspace, pol.MaxToolCallsPerStep, session, mapper)

	loop = &core.Loop{
		Run:         run,
		Provider:    prov,
		Tools:       reg,
		Policy:      pol,
		Trace:       trace,
		Budget:      budget,
		ConfirmFn:   confirmFn,
		OutputFn:    tuiOutputFn,
		GenParams:   genParams,
		Solver:      solver,
		Session:     session,
		Mapper:      mapper,
		NetworkTier: loopNetworkTier(cfg),
		Snapshots:   buildSnapshotManager(cfg, workspace),
	}

	// Start Bubble Tea first: Program.Send blocks before Run initializes.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- tui.Run()
	}()

	tracedWorkspace := workspace
	if cfg.Sandbox.Enabled {
		tracedWorkspace = "/workspace"
	}

	metadata, _ := prov.Metadata(ctx, model)
	loop.ModelMetadata = metadata
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:        pol.Name,
		Provider:      prov.Name(),
		Model:         model,
		Workspace:     tracedWorkspace,
		ModelMetadata: metadata,
		}); err != nil {
			if logger != nil {
				logger.Printf("emit run_start error: %v", err)
			}
			tui.Quit()
			<-runErrCh
			return err
		}

	if logger != nil {
		logger.Printf("run_start emitted; waiting for tui loop")
	}

	if initialPrompt != "" {
		go func() {
			time.Sleep(100 * time.Millisecond) // Give TUI a moment to start
			if err := loop.Step(ctx, initialPrompt); err != nil {
				var budgetErr *core.ErrBudgetExceeded
				if errors.As(err, &budgetErr) {
					reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
					tui.Quit()
				}
			}
		}()
	}

	if err := <-runErrCh; err != nil {
		if logger != nil {
			logger.Printf("tui run error: %v", err)
		}
		return err
	}

	if logger != nil {
		logger.Printf("tui loop ended reason=%s", reason)
	}
	_ = loop.EmitRunEnd(reason)

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		if logger != nil {
			logger.Printf("sandbox finalize error: %v", err)
		}
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}

	return nil
}

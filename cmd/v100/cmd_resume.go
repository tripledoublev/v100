package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func resumeCmd(cfgPath *string) *cobra.Command {
	var (
		tuiFlag          bool
		tuiNoAltFlag     bool
		tuiPlainFlag     bool
		tuiDebugFlag     bool
		autoFlag         bool
		unsafeFlag       bool
		yoloFlag         bool
		sandboxFlag      bool
		confirmToolsFlag string
		workspaceFlag    string
		budgetStepsFlag  int
		budgetTokensFlag int
		budgetCostFlag   float64
	)

	cmd := &cobra.Command{
		Use:          "resume <run_id>",
		Short:        "Resume an existing run from its trace",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]

			// Find run directory
			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			tracePath := filepath.Join(runDir, "trace.jsonl")
			events, err := core.ReadAll(tracePath)
			if err != nil {
				return fmt.Errorf("read trace: %w", err)
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			// Reconstruct message history from trace
			msgs, providerName, model, tracedWorkspace, metadata := reconstructHistory(runDir, events)

			// Load meta to get original environment grounding
			meta, _ := core.ReadMeta(runDir)

			// 1. Inherit sandbox config from meta if present
			if meta.Sandbox.Enabled || meta.Sandbox.Backend != "" {
				cfg.Sandbox = meta.Sandbox
			}
			// 2. Allow CLI flags to override
			if cmd.Flags().Changed("sandbox") {
				cfg.Sandbox.Enabled = sandboxFlag
			}

			if providerName == "" {
				providerName = cfg.Defaults.Provider
			}

			prov, err := buildProvider(cfg, providerName)
			if err != nil {
				return err
			}

			reg := buildToolRegistry(cfg)
			if err := validateToolRegistry(reg); err != nil {
				return err
			}
			pol := loadPolicy(cfg, "default")
			if cfg.Defaults.ContextLimit > 0 {
				pol.ContextLimit = cfg.Defaults.ContextLimit
			}

			// Build budget with overrides
			maxSteps := budgetStepsFlag
			if maxSteps == 0 {
				maxSteps = cfg.Defaults.BudgetSteps
			}
			maxTokens := budgetTokensFlag
			if maxTokens == 0 {
				maxTokens = cfg.Defaults.BudgetTokens
			}
			maxCost := budgetCostFlag
			if maxCost == 0 {
				maxCost = cfg.Defaults.BudgetCostUSD
			}

			if yoloFlag {
				autoFlag = true
				unsafeFlag = true
			}
			if confirmToolsFlag != "" {
				cfg.Defaults.ConfirmTools = confirmToolsFlag
			}
			if autoFlag {
				cfg.Defaults.ConfirmTools = "never"
			}
			if err := validateExecutionSafety(cfg, cfg.Defaults.ConfirmTools, unsafeFlag); err != nil {
				return err
			}

			budget := core.NewBudgetTracker(&core.Budget{
				MaxSteps:   maxSteps,
				MaxTokens:  maxTokens,
				MaxCostUSD: maxCost,
			})

			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return err
			}
			defer func() { _ = trace.Close() }()

			run := &core.Run{
				ID:        runID,
				Dir:       runDir,
				TraceFile: tracePath,
			}
			sourceWorkspace := resolveResumeSourceWorkspace(workspaceFlag, runDir, tracedWorkspace, meta)

			execFactory, err := executor.NewExecutor(cfg.Sandbox, filepath.Dir(runDir))
			if err != nil {
				return err
			}
			session, err := execFactory.NewSession(runID, sourceWorkspace)
			if err != nil {
				return err
			}

			sandboxWorkspace := sourceWorkspace
			if cfg.Sandbox.Enabled {
				sandboxWorkspace = filepath.Join(filepath.Dir(runDir), runID, "workspace")
				if _, err := os.Stat(sandboxWorkspace); err != nil {
					return fmt.Errorf("resume sandbox workspace: %w", err)
				}
				defer func() { _ = session.Close() }()
			}

			mapper := core.NewPathMapper(sourceWorkspace, sandboxWorkspace)
			run.Dir = sandboxWorkspace
			pol.MemoryPath = filepath.Join(sandboxWorkspace, "MEMORY.md")

			if tuiFlag {
				return resumeWithTUI(cfg, run, prov, reg, pol, trace, budget, model, events, msgs, sandboxWorkspace, !tuiNoAltFlag, tuiPlainFlag, tuiDebugFlag, session, mapper, metadata)
			}
			return resumeWithCLI(cfg, run, prov, reg, pol, trace, budget, model, events, msgs, sandboxWorkspace, session, mapper, metadata)
		},
	}
	cmd.Flags().BoolVar(&tuiFlag, "tui", false, "enable Bubble Tea TUI")
	cmd.Flags().BoolVar(&tuiNoAltFlag, "tui-no-alt", false, "disable alternate screen mode in TUI (for terminal compatibility)")
	cmd.Flags().BoolVar(&tuiPlainFlag, "tui-plain", false, "force plain monochrome TUI rendering for terminal compatibility")
	cmd.Flags().BoolVar(&tuiDebugFlag, "tui-debug", false, "write TUI startup/runtime debug log to run directory")
	cmd.Flags().BoolVar(&autoFlag, "auto", false, "auto-approve all tool calls (no confirmation)")
	cmd.Flags().BoolVar(&unsafeFlag, "unsafe", false, "acknowledge host workspace risk (required with --auto outside sandbox)")
	cmd.Flags().BoolVar(&yoloFlag, "yolo", false, "shorthand for --auto --unsafe")
	cmd.Flags().BoolVar(&sandboxFlag, "sandbox", false, "enable sandbox for resumed run (inherited from meta.json by default)")
	cmd.Flags().StringVar(&confirmToolsFlag, "confirm-tools", "", "confirm mode: always|dangerous|never")
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "workspace directory for tool operations (overrides traced workspace)")
	cmd.Flags().IntVar(&budgetStepsFlag, "budget-steps", 0, "max steps (0=config default)")
	cmd.Flags().IntVar(&budgetTokensFlag, "budget-tokens", 0, "max tokens (0=config default)")
	cmd.Flags().Float64Var(&budgetCostFlag, "budget-cost", 0, "max cost in USD (0=config default)")
	return cmd
}

func resumeWithCLI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model string, events []core.Event, msgs []providers.Message, workspace string, session executor.Session, mapper *core.PathMapper, metadata providers.ModelMetadata) error {

	renderer := ui.NewCLIRenderer()
	outputFn := core.OutputFn(renderer.RenderEvent)
	registerAgentTool(cfg, reg, trace, budget, &outputFn, buildConfirmFn(cfg.Defaults.ConfirmTools), workspace, pol.MaxToolCallsPerStep, session, mapper)

	loop := &core.Loop{
		Run:              run,
		Provider:         prov,
		CompressProvider: buildCompressProvider(cfg),
		Tools:            reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		Messages:         msgs,
		ConfirmFn:        buildConfirmFn(cfg.Defaults.ConfirmTools),
		OutputFn:         outputFn,
		Session:          session,
		Mapper:           mapper,
		ModelMetadata:    metadata,
		NetworkTier:      loopNetworkTier(cfg),
		Snapshots:        buildSnapshotManager(cfg, workspace),
	}
	loop.OutputFn = outputFn
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

	fmt.Println(ui.Info(fmt.Sprintf("Resuming run %s  (%d events loaded)", run.ID, len(events))))
	// Fix #8: Show resume context banner with provider/model/budget/context count
	fmt.Println(ui.Info(ui.Dim("provider: ") + prov.Name() + ui.Dim(" · ") + model))
	fmt.Println(ui.Info(ui.Dim("budget: ") + budget.Summary()))
	fmt.Println(ui.Info(ui.Dim("context: ") + fmt.Sprintf("%d messages", len(msgs))))
	fmt.Println(ui.Info(ui.Dim("workspace: ") + workspace))

	ctx := context.Background()
	reason := "user_exit"
	for {
		input, err := ui.Prompt("")
		if err != nil {
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
				reason = "budget_exceeded"
				break
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	_ = loop.EmitRunEnd(reason, "")
	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		fmt.Fprintln(os.Stderr, ui.Warn("sandbox finalize: "+err.Error()))
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}
	return nil
}

func resumeWithTUI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model string, events []core.Event, msgs []providers.Message, workspace string, useAltScreen bool, plainTTY bool, debug bool, session executor.Session, mapper *core.PathMapper, metadata providers.ModelMetadata) error {

	var logger *log.Logger
	if debug {
		logPath := filepath.Join(filepath.Dir(run.TraceFile), "tui.resume.debug.log")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer func() { _ = f.Close() }()
			logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
			logger.Printf("resume run_id=%s provider=%s model=%s alt=%t plain=%t", run.ID, prov.Name(), model, useAltScreen, plainTTY)
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
				_ = loop.EmitRunEnd("budget_exceeded", "")
				tui.Quit()
			}
		}
	}

	tui = ui.NewTUI(submitFn, useAltScreen, plainTTY)

	confirmFn := func(toolName, args string) bool {
		if cfg.Defaults.ConfirmTools == "never" {
			return true
		}
		if cfg.Defaults.ConfirmTools == "always" || (cfg.Defaults.ConfirmTools == "dangerous" && reg.IsDangerous(toolName)) {
			return tui.RequestConfirm(toolName, args)
		}
		return true
	}

	tuiOutputFn := core.OutputFn(func(ev core.Event) { tui.SendEvent(ev) })
	registerAgentTool(cfg, reg, trace, budget, &tuiOutputFn, confirmFn, workspace, pol.MaxToolCallsPerStep, session, mapper)

	loop = &core.Loop{
		Run:              run,
		Provider:         prov,
		CompressProvider: buildCompressProvider(cfg),
		Tools:            reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		Messages:         msgs,
		ConfirmFn:        confirmFn,
		OutputFn:         tuiOutputFn,
		Session:          session,
		Mapper:           mapper,
		ModelMetadata:    metadata,
		NetworkTier:      loopNetworkTier(cfg),
		Snapshots:        buildSnapshotManager(cfg, workspace),
	}
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

	// Start Bubble Tea first: Program.Send blocks until Run() starts the event loop.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- tui.Run()
	}()

	// Wait for TUI event loop to be ready before sending any events.
	tui.WaitReady()

	// Feed historical events into TUI
	go func() {
		for _, ev := range events {
			tui.SendEvent(ev)
		}
		if logger != nil {
			logger.Printf("fed %d historical events to TUI", len(events))
		}
	}()

	if err := <-runErrCh; err != nil {
		if logger != nil {
			logger.Printf("tui run error: %v", err)
		}
		return err
	}

	if logger != nil {
		logger.Printf("tui loop ended reason=%s", reason)
	}
	_ = loop.EmitRunEnd(reason, "")

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		if logger != nil {
			logger.Printf("sandbox finalize error: %v", err)
		}
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}

	return nil
}

func resolveResumeSourceWorkspace(workspaceFlag, runDir, tracedWorkspace string, meta core.RunMeta) string {
	if strings.TrimSpace(workspaceFlag) != "" {
		return resolveWorkspace(workspaceFlag, runDir)
	}
	if strings.TrimSpace(meta.SourceWorkspace) != "" {
		return meta.SourceWorkspace
	}
	if strings.TrimSpace(tracedWorkspace) != "" {
		if tracedWorkspace == "/workspace" {
			return resolveWorkspace("", runDir)
		}
		return resolveWorkspace(tracedWorkspace, runDir)
	}
	return resolveWorkspace("", runDir)
}

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

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func runWithTUI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, useAltScreen bool, plainTTY bool, debug bool, verbose bool, genParams providers.GenParams, solver core.Solver, initialPrompt string, session executor.Session, mapper *core.PathMapper) error {

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
	tui.SetVerbose(verbose)

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

	// metadata auto-discovery
	metadata, _ := prov.Metadata(ctx, model)
	loop.ModelMetadata = metadata
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:        pol.Name,
		Provider:      prov.Name(),
		Model:         model,
		Workspace:     workspace,
		ModelMetadata: metadata,
	}); err != nil {
		return err
	}

	// Start Bubble Tea first: Program.Send blocks before Run initializes.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- tui.Run()
	}()

	if initialPrompt != "" {
		// Give TUI a tiny moment to start before first step
		time.Sleep(100 * time.Millisecond)
		if err := loop.Step(ctx, initialPrompt); err != nil {
			if logger != nil {
				logger.Printf("initial step error: %v", err)
			}
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				reason = "budget_exceeded"
			} else {
				reason = "error"
			}
			tui.Quit()
		}
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

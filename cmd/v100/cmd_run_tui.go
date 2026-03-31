package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	var stepCancel context.CancelFunc
	var stepMu sync.Mutex

	submitFn := func(req ui.SubmitRequest) {
		if logger != nil {
			logger.Printf("submit input_len=%d images=%d", len(req.Text), len(req.Images))
		}
		inputTrim := strings.TrimSpace(req.Text)
		if inputTrim == "/quit" || inputTrim == "/exit" {
			reason = "user_exit"
			tui.Quit()
			return
		}

		stepMu.Lock()
		if stepCancel != nil {
			stepCancel()
		}
		var stepCtx context.Context
		stepCtx, stepCancel = context.WithCancel(ctx)
		stepMu.Unlock()

		defer func() {
			stepMu.Lock()
			stepCancel = nil
			stepMu.Unlock()
		}()

		images := make([]providers.ImageAttachment, 0, len(req.Images))
		for _, img := range req.Images {
			images = append(images, providers.ImageAttachment{MIMEType: "image/png", Data: img})
		}

		err := runInteractiveStep(func() error {
			return loop.StepWithImages(stepCtx, req.Text, images)
		}, budget, func(reason string) bool {
			return tui.RequestConfirm(interactiveBudgetLabel(reason), interactiveBudgetConfirmMessage(reason))
		})
		if err == nil {
			return
		}
		if logger != nil {
			logger.Printf("step error: %v", err)
		}
		if errors.Is(err, context.Canceled) {
			// User interrupted intentionally; don't emit error event
			return
		}
		var budgetErr *core.ErrBudgetExceeded
		if errors.As(err, &budgetErr) {
			reason = "budget_exceeded"
			_ = emitFinalTUIRunEnd(loop, prov, model, reason)
			tui.Quit()
			return
		}
		var retryErr *providers.RetryableError
		var retryBudgetErr *providers.RetryBudgetExceededError
		if errors.As(err, &retryBudgetErr) {
			reason = classifyProviderFailureReason(err)
			_ = emitFinalTUIRunEnd(loop, prov, model, reason)
			tui.Quit()
			return
		}
		if errors.As(err, &retryErr) {
			reason = classifyProviderFailureReason(err)
			_ = emitFinalTUIRunEnd(loop, prov, model, reason)
			tui.Quit()
			return
		}
	}

	interruptFn := func() {
		stepMu.Lock()
		if stepCancel != nil {
			if logger != nil {
				logger.Printf("interrupting active step")
			}
			stepCancel()
			stepCancel = nil
		}
		stepMu.Unlock()
	}

	tui = ui.NewTUI(submitFn, useAltScreen, plainTTY)
	tui.SetInterruptFn(interruptFn)
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
		Run:              run,
		Provider:         prov,
		CompressProvider: buildCompressProvider(cfg),
		Tools:            reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		ConfirmFn:        confirmFn,
		OutputFn:         tuiOutputFn,
		GenParams:        genParams,
		Solver:           solver,
		Session:          session,
		Mapper:           mapper,
		NetworkTier:      loopNetworkTier(cfg),
		Snapshots:        buildSnapshotManager(cfg, workspace),
	}

	// metadata auto-discovery
	metadata, _ := prov.Metadata(ctx, model)
	loop.ModelMetadata = metadata
	persistModelMetadata(filepath.Dir(run.TraceFile), metadata)

	// Start Bubble Tea first: Program.Send blocks until Run() starts the event loop.
	runErrCh := make(chan error, 1)
	if logger != nil {
		logger.Printf("starting tui.Run goroutine")
	}
	go func() {
		if logger != nil {
			logger.Printf("inside tui.Run goroutine")
		}
		err := tui.Run()
		if logger != nil {
			logger.Printf("tui.Run returned err=%v", err)
		}
		runErrCh <- err
	}()

	// Wait for TUI event loop to be ready before sending any events.
	if logger != nil {
		logger.Printf("waiting for tui to be ready")
	}
	tui.WaitReady()
	if logger != nil {
		logger.Printf("tui is ready")
	}

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:        pol.Name,
		Provider:      prov.Name(),
		Model:         model,
		Workspace:     workspace,
		ModelMetadata: metadata,
	}); err != nil {
		return err
	}

	if initialPrompt != "" {
		if logger != nil {
			logger.Printf("processing initial prompt: %q", initialPrompt)
		}

		stepMu.Lock()
		var stepCtx context.Context
		stepCtx, stepCancel = context.WithCancel(ctx)
		stepMu.Unlock()

		err := runInteractiveStep(func() error {
			return loop.Step(stepCtx, initialPrompt)
		}, budget, func(reason string) bool {
			return tui.RequestConfirm(interactiveBudgetLabel(reason), interactiveBudgetConfirmMessage(reason))
		})

		stepMu.Lock()
		stepCancel = nil
		stepMu.Unlock()

		if err != nil {
			if logger != nil {
				logger.Printf("initial step error: %v", err)
			}
			if errors.Is(err, context.Canceled) {
				// User interrupted intentionally; don't emit error event, keep user_exit reason
			} else {
				var budgetErr *core.ErrBudgetExceeded
				if errors.As(err, &budgetErr) {
					reason = "budget_exceeded"
				} else {
					var retryErr *providers.RetryableError
					var retryBudgetErr *providers.RetryBudgetExceededError
					if errors.As(err, &retryBudgetErr) {
						reason = classifyProviderFailureReason(err)
					} else if errors.As(err, &retryErr) {
						reason = classifyProviderFailureReason(err)
					} else {
						reason = "error"
					}
				}
				_ = emitFinalTUIRunEnd(loop, prov, model, reason)
				tui.Quit()
			}
		}
	} else {
		if logger != nil {
			logger.Printf("no initial prompt, waiting for user input")
		}
	}

	if logger != nil {
		logger.Printf("waiting for tui to finish")
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

	// Generate summary if possible using the run's own provider
	finalSummary := ""
	if len(loop.Messages) > 1 && reason != "error" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sumReq := providers.CompleteRequest{
			Model: model,
			Messages: append(loop.Messages, providers.Message{
				Role:    "user",
				Content: "Briefly summarize the outcome of this run in one sentence (max 20 words). What was achieved?",
			}),
		}
		if resp, err := prov.Complete(ctx, sumReq); err == nil {
			finalSummary = strings.TrimSpace(resp.AssistantText)
		}
	}

	if finalSummary == "" {
		if err := emitFinalTUIRunEnd(loop, prov, model, reason); err != nil {
			return err
		}
	} else if err := loop.EmitRunEnd(reason, finalSummary); err != nil {
		return err
	}

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		if logger != nil {
			logger.Printf("sandbox finalize error: %v", err)
		}
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}
	maybePrintFailureDigest(os.Stderr, trace.Path(), reason)

	return nil
}

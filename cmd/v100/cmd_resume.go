package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/ui"
)

func resumeCmd(cfgPath *string) *cobra.Command {
	var tuiFlag bool
	var workspaceFlag string

	cmd := &cobra.Command{
		Use:   "resume <run_id>",
		Short: "Resume an existing run from its trace",
		Args:  cobra.ExactArgs(1),
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
			msgs, providerName, model, tracedWorkspace := reconstructHistory(events)

			// Load meta to get original source workspace
			meta, _ := core.ReadMeta(runDir)

			if providerName == "" {
				providerName = cfg.Defaults.Provider
			}

			prov, err := buildProvider(cfg, providerName)
			if err != nil {
				return err
			}

			reg := buildToolRegistry(cfg)
			pol := loadPolicy(cfg, "default")
			if cfg.Defaults.ContextLimit > 0 {
				pol.ContextLimit = cfg.Defaults.ContextLimit
			}
			budget := core.NewBudgetTracker(&core.Budget{
				MaxSteps:  cfg.Defaults.BudgetSteps,
				MaxTokens: cfg.Defaults.BudgetTokens,
			})

			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return err
			}
			defer trace.Close()

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
				defer session.Close()
			}

			mapper := core.NewPathMapper(sourceWorkspace, sandboxWorkspace)
			run.Dir = sandboxWorkspace
			pol.MemoryPath = filepath.Join(sandboxWorkspace, "MEMORY.md")

			renderer := ui.NewCLIRenderer()
			outputFn := core.OutputFn(renderer.RenderEvent)
			registerAgentTool(cfg, reg, trace, budget, &outputFn, buildConfirmFn(cfg.Defaults.ConfirmTools), sandboxWorkspace, pol.MaxToolCallsPerStep, session, mapper)

			loop := &core.Loop{
				Run:       run,
				Provider:  prov,
				Tools:     reg,
				Policy:    pol,
				Trace:     trace,
				Budget:    budget,
				Messages:  msgs,
				ConfirmFn: buildConfirmFn(cfg.Defaults.ConfirmTools),
				OutputFn:  outputFn,
				Session:   session,
				Mapper:    mapper,
			}
			loop.OutputFn = outputFn

			fmt.Println(ui.Info(fmt.Sprintf("Resuming run %s  (%d events loaded)", runID, len(events))))
			fmt.Println(ui.Info(ui.Dim("workspace: ") + sandboxWorkspace))
			_ = model
			_ = tuiFlag

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
				if err := loop.Step(ctx, input); err != nil {
					var budgetErr *core.ErrBudgetExceeded
					if errors.As(err, &budgetErr) {
						reason = "budget_exceeded"
						break
					}
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
			}
			_ = loop.EmitRunEnd(reason)
			return nil
		},
	}
	cmd.Flags().BoolVar(&tuiFlag, "tui", false, "enable TUI")
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "workspace directory for tool operations (overrides traced workspace)")
	return cmd
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

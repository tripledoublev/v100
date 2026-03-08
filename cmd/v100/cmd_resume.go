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
			// Source workspace grounding
			sourceWorkspace := resolveWorkspace(workspaceFlag, runDir)
			if workspaceFlag == "" && strings.TrimSpace(tracedWorkspace) != "" {
				// If we resumed a sandboxed run, the traced workspace is "/workspace"
				// but we need the real host source workspace.
				// In a real implementation, we'd store the source path in meta.json.
				if tracedWorkspace == "/workspace" {
					// for now assume current host source is same as original
					sourceWorkspace = resolveWorkspace("", runDir)
				} else {
					sourceWorkspace = resolveWorkspace(tracedWorkspace, runDir)
				}
			}

			// Build sandbox session (always same type as default for now)
			execFactory, _ := executor.NewExecutor(cfg.Sandbox, filepath.Dir(runDir))
			session, _ := execFactory.NewSession(runID, sourceWorkspace)
			
			sandboxWorkspace := sourceWorkspace
			// In resume, we might need to verify if the sandbox already exists
			if cfg.Sandbox.Enabled {
				// Re-start or just resolve path
				sandboxWorkspace = filepath.Join(filepath.Dir(runDir), runID, "workspace")
			}

			mapper := core.NewPathMapper(sourceWorkspace, sandboxWorkspace)
			run.Dir = sandboxWorkspace
			pol.MemoryPath = filepath.Join(sandboxWorkspace, "MEMORY.md")

			loop := &core.Loop{
				Run:       run,
				Provider:  prov,
				Tools:     reg,
				Policy:    pol,
				Trace:     trace,
				Budget:    budget,
				Messages:  msgs,
				ConfirmFn: buildConfirmFn(cfg.Defaults.ConfirmTools),
				Session:   session,
				Mapper:    mapper,
			}

			renderer := ui.NewCLIRenderer()
			loop.OutputFn = renderer.RenderEvent

			fmt.Println(ui.Info(fmt.Sprintf("Resuming run %s  (%d events loaded)", runID, len(events))))
			fmt.Println(ui.Info(ui.Dim("workspace: ") + workspace))
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

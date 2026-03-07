package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/auth"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// ─────────────────────────────────────────
// Root command
// ─────────────────────────────────────────

func rootCmd() *cobra.Command {
	var cfgPath string

	root := &cobra.Command{
		Use:   "v100",
		Short: "Modular CLI/TUI agent harness",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: ~/.config/v100/config.toml)")

	root.AddCommand(
		runCmd(&cfgPath),
		resumeCmd(&cfgPath),
		replayCmd(&cfgPath),
		toolsCmd(&cfgPath),
		providersCmd(&cfgPath),
		configInitCmd(),
		doctorCmd(&cfgPath),
		loginCmd(),
		logoutCmd(),
		devCmd(),
		scoreCmd(),
		statsCmd(),
		metricsCmd(),
		compareCmd(),
		benchCmd(&cfgPath),
		queryCmd(),
	)
	return root
}

// ─────────────────────────────────────────
// agent run
// ─────────────────────────────────────────

func runCmd(cfgPath *string) *cobra.Command {
	var (
		providerFlag     string
		modelFlag        string
		policyFlag       string
		runDirFlag       string
		workspaceFlag    string
		unsafeFlag       bool
		autoFlag         bool
		budgetStepsFlag  int
		budgetTokensFlag int
		budgetCostFlag   float64
		toolTimeoutFlag  int
		maxToolCallsFlag int
		confirmToolsFlag string
		tuiFlag          bool
		tuiNoAltFlag     bool
		tuiPlainFlag     bool
		tuiDebugFlag     bool
		nameFlag         string
		tagFlags         []string
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

			// Write meta.json
			tags := parseTags(tagFlags)
			meta := core.RunMeta{
				RunID:     runID,
				Name:      nameFlag,
				Tags:      tags,
				Provider:  cfg.Defaults.Provider,
				Model:     modelFlag,
				CreatedAt: time.Now().UTC(),
			}
			if meta.Model == "" {
				if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
					meta.Model = pc.DefaultModel
				}
			}
			if meta.Provider == "" {
				meta.Provider = cfg.Defaults.Provider
			}
			_ = core.WriteMeta(runDir, meta)

			tracePath := filepath.Join(runDir, "trace.jsonl")
			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return err
			}
			defer trace.Close()

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

			// Set workspace
			workspace := resolveWorkspace(workspaceFlag, runDir)

			// Build provider
			prov, err := buildProvider(cfg, cfg.Defaults.Provider)
			if err != nil {
				return err
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

			// Override workspace for tools
			_ = workspace

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

			// Model
			model := modelFlag
			if model == "" {
				if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
					model = pc.DefaultModel
				}
			}

			if tuiFlag {
				return runWithTUI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, !tuiNoAltFlag, tuiPlainFlag, tuiDebugFlag)
			}
			return runWithCLI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace)
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider name (overrides config)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model name (overrides config)")
	cmd.Flags().StringVar(&policyFlag, "policy", "default", "policy name")
	cmd.Flags().StringVar(&runDirFlag, "run-dir", "", "base directory for runs (default: ./runs)")
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "workspace directory for tool operations")
	cmd.Flags().BoolVar(&unsafeFlag, "unsafe", false, "disable path guardrails and confirmations")
	cmd.Flags().BoolVar(&autoFlag, "auto", false, "auto-approve all tool calls (no confirmation)")
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

	return cmd
}

func runWithCLI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string) error {

	renderer := ui.NewCLIRenderer()

	confirmFn := buildConfirmFn(confirmMode)

	outputFn := core.OutputFn(renderer.RenderEvent)
	registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, workspace, pol.MaxToolCallsPerStep)

	loop := &core.Loop{
		Run:       run,
		Provider:  prov,
		Tools:     reg,
		Policy:    pol,
		Trace:     trace,
		Budget:    budget,
		ConfirmFn: confirmFn,
		OutputFn:  outputFn,
	}

	// Override workspace for tool execution
	run.Dir = workspace

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:    pol.Name,
		Provider:  prov.Name(),
		Model:     model,
		Workspace: workspace,
	}); err != nil {
		return err
	}

	fmt.Println(ui.Info(ui.Dim("trace: ") + run.TraceFile))
	fmt.Println(ui.Info(ui.Dim("workspace: ") + workspace))
	fmt.Println(ui.Info(ui.Dim("budget: ") + budget.Summary()))
	fmt.Println(ui.Dim("Ctrl+C or /quit to exit"))

	ctx := context.Background()
	reason := "user_exit"

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
	fmt.Println(ui.Dim("budget: " + budget.Summary()))
	return nil
}

func runWithTUI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, useAltScreen bool, plainTTY bool, debug bool) error {

	run.Dir = workspace

	var logger *log.Logger
	if debug {
		logPath := filepath.Join(filepath.Dir(run.TraceFile), "tui.debug.log")
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer f.Close()
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
	registerAgentTool(cfg, reg, trace, budget, &tuiOutputFn, confirmFn, workspace, pol.MaxToolCallsPerStep)

	loop = &core.Loop{
		Run:       run,
		Provider:  prov,
		Tools:     reg,
		Policy:    pol,
		Trace:     trace,
		Budget:    budget,
		ConfirmFn: confirmFn,
		OutputFn:  tuiOutputFn,
	}

	// Start Bubble Tea first: Program.Send blocks before Run initializes.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- tui.Run()
	}()

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:   pol.Name,
		Provider: prov.Name(),
		Model:    model,
	}); err != nil {
		if logger != nil {
			logger.Printf("emit run_start error: %v", err)
		}
		tui.Quit()
		_ = <-runErrCh
		return err
	}

	if logger != nil {
		logger.Printf("run_start emitted; waiting for tui loop")
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
	return nil
}

// ─────────────────────────────────────────
// agent resume
// ─────────────────────────────────────────

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
			workspace := resolveWorkspace(workspaceFlag, runDir)
			if workspaceFlag == "" && strings.TrimSpace(tracedWorkspace) != "" {
				workspace = resolveWorkspace(tracedWorkspace, runDir)
			}
			run.Dir = workspace
			pol.MemoryPath = filepath.Join(workspace, "MEMORY.md")

			loop := &core.Loop{
				Run:       run,
				Provider:  prov,
				Tools:     reg,
				Policy:    pol,
				Trace:     trace,
				Budget:    budget,
				Messages:  msgs,
				ConfirmFn: buildConfirmFn(cfg.Defaults.ConfirmTools),
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

// ─────────────────────────────────────────
// agent replay
// ─────────────────────────────────────────

func replayCmd(cfgPath *string) *cobra.Command {
	var deterministic bool
	var stepMode bool
	var replaceModel string
	var injectTool []string

	cmd := &cobra.Command{
		Use:   "replay <run_id>",
		Short: "Pretty-print a run trace as a readable transcript",
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

			if deterministic {
				cfg, err := loadConfig(*cfgPath)
				if err != nil {
					return err
				}
				injected, err := parseInjectedToolOutputs(injectTool)
				if err != nil {
					return err
				}
				return deterministicReplay(cmd.Context(), cfg, events, deterministicReplayOptions{
					StepMode:     stepMode,
					ReplaceModel: strings.TrimSpace(replaceModel),
					InjectTools:  injected,
				})
			}

			for _, ev := range events {
				ui.PrintReplayEvent(ev)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&deterministic, "deterministic", false, "replay recorded model/tool events deterministically")
	cmd.Flags().BoolVar(&stepMode, "step", false, "pause between deterministic replay events")
	cmd.Flags().StringVar(&replaceModel, "replace-model", "", "run recorded model.call events against this model and show counterfactual responses")
	cmd.Flags().StringArrayVar(&injectTool, "inject-tool", nil, "inject tool output during deterministic replay: name=output (repeatable)")
	return cmd
}

type deterministicReplayOptions struct {
	StepMode     bool
	ReplaceModel string
	InjectTools  map[string]string
}

func deterministicReplay(ctx context.Context, cfg *config.Config, events []core.Event, opts deterministicReplayOptions) error {
	fmt.Println(ui.Header("Deterministic Replay"))
	fmt.Println(ui.Dim("model/tool outputs are sourced from trace records only"))
	if opts.ReplaceModel != "" {
		fmt.Println(ui.Info("counterfactual model override: " + opts.ReplaceModel))
	}
	if len(opts.InjectTools) > 0 {
		fmt.Println(ui.Info(fmt.Sprintf("tool injections active: %d", len(opts.InjectTools))))
	}

	hasModelCall := false
	reader := bufio.NewReader(os.Stdin)
	var prov providers.Provider
	var providerName string
	var counterfactualQ []providers.CompleteResponse
	var counterfactualErrQ []error

	pause := func(label string) error {
		if !opts.StepMode {
			return nil
		}
		fmt.Print(ui.Dim("Press Enter to continue (" + label + ")..."))
		_, err := reader.ReadString('\n')
		return err
	}

	for i, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if strings.TrimSpace(p.Provider) != "" {
				providerName = p.Provider
			}
			ui.PrintReplayEvent(ev)

		case core.EventModelCall:
			hasModelCall = true
			var p core.ModelCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] MODEL CALL  step=%s", i+1, shortID(ev.StepID))))
			fmt.Printf("%s\n", ui.Dim(fmt.Sprintf("tools=%d  max_tool_calls=%d", len(p.ToolNames), p.MaxToolCalls)))
			msgs := applyInjectedToolOutputs(p.Messages, opts.InjectTools)
			for _, m := range msgs {
				content := strings.TrimSpace(m.Content)
				if len(content) > 200 {
					content = content[:200] + "…"
				}
				content = strings.ReplaceAll(content, "\n", " ")
				if content == "" && len(m.ToolCalls) > 0 {
					content = fmt.Sprintf("[assistant tool-calls: %d]", len(m.ToolCalls))
				}
				fmt.Printf("  %s: %s\n", m.Role, content)
			}

			if opts.ReplaceModel != "" {
				if prov == nil {
					pn := providerName
					if strings.TrimSpace(pn) == "" {
						pn = cfg.Defaults.Provider
					}
					var err error
					prov, err = buildProvider(cfg, pn)
					if err != nil {
						counterfactualQ = append(counterfactualQ, providers.CompleteResponse{})
						counterfactualErrQ = append(counterfactualErrQ, err)
					}
				}
				if prov != nil {
					toolSpecs := replayToolSpecs(cfg, p.ToolNames)
					resp, err := prov.Complete(ctx, providers.CompleteRequest{
						RunID:    ev.RunID,
						StepID:   ev.StepID,
						Messages: msgs,
						Tools:    toolSpecs,
						Model:    opts.ReplaceModel,
						Hints: providers.Hints{
							MaxToolCalls: p.MaxToolCalls,
						},
					})
					counterfactualQ = append(counterfactualQ, resp)
					counterfactualErrQ = append(counterfactualErrQ, err)
				}
			}
			if err := pause("model.call"); err != nil {
				return err
			}

		case core.EventModelResp:
			var p core.ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] MODEL RESPONSE (recorded)", i+1)))
			if strings.TrimSpace(p.Text) != "" {
				fmt.Println(p.Text)
			}
			if len(p.ToolCalls) > 0 {
				fmt.Println(ui.Dim(fmt.Sprintf("tool_calls=%d", len(p.ToolCalls))))
			}
			if opts.ReplaceModel != "" && len(counterfactualQ) > 0 && len(counterfactualErrQ) > 0 {
				cf := counterfactualQ[0]
				cerr := counterfactualErrQ[0]
				counterfactualQ = counterfactualQ[1:]
				counterfactualErrQ = counterfactualErrQ[1:]
				fmt.Printf("\n%s\n", styleReplayTitle("COUNTERFACTUAL RESPONSE"))
				if cerr != nil {
					fmt.Println(ui.Fail("counterfactual model call failed: " + cerr.Error()))
				} else {
					if strings.TrimSpace(cf.AssistantText) != "" {
						fmt.Println(cf.AssistantText)
					} else {
						fmt.Println(ui.Dim("(empty assistant text)"))
					}
					if len(cf.ToolCalls) > 0 {
						fmt.Println(ui.Dim(fmt.Sprintf("counterfactual tool_calls=%d", len(cf.ToolCalls))))
					}
				}
			}
			if err := pause("model.response"); err != nil {
				return err
			}

		case core.EventToolCall:
			var p core.ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] TOOL CALL (recorded) %s", i+1, p.Name)))
			fmt.Println(p.Args)
			if err := pause("tool.call"); err != nil {
				return err
			}

		case core.EventToolResult:
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] TOOL RESULT (recorded) %s  ok=%t", i+1, p.Name, p.OK)))
			out := p.Output
			if inj, ok := opts.InjectTools[p.Name]; ok {
				out = inj
				fmt.Println(ui.Warn("injected tool output override applied"))
			}
			if len(out) > 500 {
				out = out[:500] + "…"
			}
			fmt.Println(out)
			if err := pause("tool.result"); err != nil {
				return err
			}

		default:
			// Keep compatibility with all existing event types.
			ui.PrintReplayEvent(ev)
		}
	}

	if !hasModelCall {
		fmt.Println(ui.Warn("trace has no model.call events; rerun with newer v100 to capture deterministic prompts"))
	}
	return nil
}

func replayToolSpecs(cfg *config.Config, names []string) []providers.ToolSpec {
	if len(names) == 0 {
		return nil
	}
	reg := buildToolRegistry(cfg)
	out := make([]providers.ToolSpec, 0, len(names))
	for _, n := range names {
		t, ok := reg.Get(n)
		if !ok {
			continue
		}
		out = append(out, providers.ToolSpec{
			Name:         t.Name(),
			Description:  t.Description(),
			InputSchema:  t.InputSchema(),
			OutputSchema: t.OutputSchema(),
		})
	}
	return out
}

func parseInjectedToolOutputs(raw []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range raw {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --inject-tool %q (want name=output)", v)
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			return nil, fmt.Errorf("invalid --inject-tool %q (empty tool name)", v)
		}
		m[name] = parts[1]
	}
	return m, nil
}

func applyInjectedToolOutputs(msgs []providers.Message, injected map[string]string) []providers.Message {
	if len(injected) == 0 {
		return msgs
	}
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		if v, ok := injected[out[i].Name]; ok {
			out[i].Content = v
		}
	}
	return out
}

func styleReplayTitle(s string) string {
	return ui.Bold(s)
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// ─────────────────────────────────────────
// agent tools
// ─────────────────────────────────────────

func toolsCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List registered tools and their schemas",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			reg := buildToolRegistry(cfg)
			ts := reg.EnabledTools()
			sort.Slice(ts, func(i, j int) bool { return ts[i].Name() < ts[j].Name() })
			for _, t := range ts {
				danger := ""
				if t.DangerLevel() == tools.Dangerous {
					danger = " [DANGEROUS]"
				}
				fmt.Printf("%-25s %s%s\n", t.Name(), t.Description(), danger)
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────
// agent providers
// ─────────────────────────────────────────

func providersCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			for name, pc := range cfg.Providers {
				fmt.Printf("%-15s type=%-10s model=%s\n", name, pc.Type, pc.DefaultModel)
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────
// agent config init
// ─────────────────────────────────────────

func configInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config init",
		Short: "Write default config template to XDG config path",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.XDGConfigPath()
			if _, err := os.Stat(path); err == nil {
				fmt.Println(ui.Warn("Config already exists at " + path))
				fmt.Print(ui.Dim("Overwrite? [y/N] "))
				var ans string
				fmt.Scanln(&ans)
				if strings.ToLower(strings.TrimSpace(ans)) != "y" {
					return nil
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(config.DefaultTOML()), 0o644); err != nil {
				return err
			}
			fmt.Println(ui.OK("Config written to " + path))

			// Also write default policy prompt
			if err := policy.WriteDefaultPrompt(); err != nil {
				fmt.Fprintln(os.Stderr, ui.Warn("could not write default policy: "+err.Error()))
			} else {
				home, _ := os.UserHomeDir()
				fmt.Println(ui.OK("Policy written to " + home + "/.config/v100/policies/default.md"))
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────
// agent login
// ─────────────────────────────────────────

func loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate via browser OAuth (ChatGPT Plus/Pro)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			fmt.Println(ui.Info("Starting OAuth login flow…"))
			t, err := auth.Login(ctx)
			if err != nil {
				return fmt.Errorf("login: %w", err)
			}
			path := auth.DefaultTokenPath()
			if err := auth.Save(path, t); err != nil {
				return fmt.Errorf("login: save token: %w", err)
			}
			fmt.Println(ui.OK("Logged in successfully"))
			if t.AccountID != "" {
				fmt.Println(ui.Dim("Account ID: ") + t.AccountID)
			}
			fmt.Println(ui.Dim("Token saved to: ") + path)
			return nil
		},
	}
}

// ─────────────────────────────────────────
// agent logout
// ─────────────────────────────────────────

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored OAuth token",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := auth.DefaultTokenPath()
			if err := os.Remove(path); err != nil {
				if os.IsNotExist(err) {
					fmt.Println(ui.Dim("Already logged out (no token found)"))
					return nil
				}
				return fmt.Errorf("logout: %w", err)
			}
			fmt.Println(ui.OK("Logged out — token removed from " + path))
			return nil
		},
	}
}

// ─────────────────────────────────────────
// agent doctor
// ─────────────────────────────────────────

func doctorCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check provider auth, tool availability, and run dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Println(ui.Header("v100 doctor"))
			fmt.Println()
			ok := true

			// 1. Config
			cfgFile := *cfgPath
			if cfgFile == "" {
				cfgFile = config.XDGConfigPath()
			}
			if _, err := os.Stat(cfgFile); err == nil {
				fmt.Println(ui.OK("Config: " + cfgFile))
			} else {
				fmt.Println(ui.Fail("Config not found at " + cfgFile + " — run: v100 config init"))
				ok = false
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				fmt.Println(ui.Fail("Config parse error: " + err.Error()))
				return nil
			}

			// 2. Provider auth
			for name, pc := range cfg.Providers {
				switch pc.Type {
				case "codex":
					tokenPath := auth.DefaultTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: no token at %s — run 'v100 login'", name, tokenPath)))
						ok = false
					}
				default:
					key := os.Getenv(pc.Auth.Env)
					if key == "" {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: env var %s not set", name, pc.Auth.Env)))
						ok = false
					} else {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: %s set (%d chars)", name, pc.Auth.Env, len(key))))
					}
				}
			}

			// 3. ripgrep
			{
				p, err := findInPath("rg")
				if err != nil || p == "" {
					fmt.Println(ui.Fail("rg (ripgrep) not found — project.search will fail"))
					ok = false
				} else {
					fmt.Println(ui.OK("rg: " + p))
				}
			}

			// 4. patch
			if p, _ := findInPath("patch"); p != "" {
				fmt.Println(ui.OK("patch: " + p))
			} else {
				fmt.Println(ui.Fail("patch not found — patch.apply will fail"))
				ok = false
			}

			// 5. git
			if p, _ := findInPath("git"); p != "" {
				fmt.Println(ui.OK("git: " + p))
			} else {
				fmt.Println(ui.Fail("git not found — git tools will fail"))
				ok = false
			}

			// 6. runs dir writable
			runsDir := "runs"
			if err := os.MkdirAll(runsDir, 0o755); err == nil {
				testFile := filepath.Join(runsDir, ".doctor_test")
				if f, err := os.Create(testFile); err == nil {
					f.Close()
					os.Remove(testFile)
					fmt.Println(ui.OK("runs/ dir writable"))
				} else {
					fmt.Println(ui.Fail("runs/ dir not writable: " + err.Error()))
					ok = false
				}
			}

			fmt.Println()
			if ok {
				fmt.Println(ui.OK(ui.Bold("All checks passed")))
			} else {
				fmt.Println(ui.Fail(ui.Bold("Some checks failed — fix issues above before running")))
			}
			return nil
		},
	}
}

// ─────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────

func loadConfig(cfgPath string) (*config.Config, error) {
	if cfgPath == "" {
		cfgPath = config.XDGConfigPath()
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return config.DefaultConfig(), nil
	}
	return config.Load(cfgPath)
}

func buildProvider(cfg *config.Config, providerName string) (providers.Provider, error) {
	pc, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	return buildProviderFromConfig(pc)
}

func buildProviderFromConfig(pc config.ProviderConfig) (providers.Provider, error) {
	switch pc.Type {
	case "codex":
		return providers.NewCodexProvider("", pc.DefaultModel)
	case "openai":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "OPENAI_API_KEY"
		}
		return providers.NewOpenAIProvider(authEnv, pc.BaseURL, pc.DefaultModel)
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
}

func buildToolRegistry(cfg *config.Config) *tools.Registry {
	reg := tools.NewRegistry(cfg.Tools.Enabled)
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.FSList())
	reg.Register(tools.FSMkdir())
	reg.Register(tools.Sh())
	reg.Register(tools.GitStatus())
	reg.Register(tools.GitDiff())
	reg.Register(tools.GitCommit())
	reg.Register(tools.GitPush())
	reg.Register(tools.CurlFetch())
	reg.Register(tools.PatchApply())
	reg.Register(tools.ProjectSearch())
	return reg
}

func registerAgentTool(cfg *config.Config, reg *tools.Registry, trace *core.TraceWriter,
	budget *core.BudgetTracker, outputFn *core.OutputFn, confirmFn core.ConfirmFn, workspace string, parentMaxToolCalls int) {

	providerBuilder := func(model string) (providers.Provider, error) {
		pc, ok := cfg.Providers[cfg.Defaults.Provider]
		if !ok {
			return nil, fmt.Errorf("provider %q not configured", cfg.Defaults.Provider)
		}
		if model != "" {
			pc.DefaultModel = model
		}
		return buildProviderFromConfig(pc)
	}

	runFn := func(ctx context.Context, params tools.AgentRunParams) tools.AgentRunResult {
		var roleCfg config.AgentConfig
		if strings.TrimSpace(params.Agent) != "" {
			cfgRole, ok := cfg.Agents[params.Agent]
			if !ok {
				return tools.AgentRunResult{OK: false, Result: "unknown agent role: " + params.Agent}
			}
			roleCfg = cfgRole
		}

		modelOverride := strings.TrimSpace(params.Model)
		if modelOverride == "" {
			modelOverride = strings.TrimSpace(roleCfg.Model)
		}

		// Build provider
		prov, err := providerBuilder(modelOverride)
		if err != nil {
			return tools.AgentRunResult{OK: false, Result: "build provider: " + err.Error()}
		}

		// Build child tool registry.
		parentTools := reg.EnabledTools()
		wantTools := make(map[string]bool)
		switch {
		case len(params.Tools) > 0:
			for _, tn := range params.Tools {
				if tn != "agent" && tn != "dispatch" {
					wantTools[tn] = true
				}
			}
		case len(roleCfg.Tools) > 0:
			for _, tn := range roleCfg.Tools {
				if tn != "agent" && tn != "dispatch" {
					wantTools[tn] = true
				}
			}
		default:
			for _, pt := range parentTools {
				if pt.Name() != "agent" && pt.Name() != "dispatch" {
					wantTools[pt.Name()] = true
				}
			}
		}

		enabledNames := make([]string, 0, len(wantTools))
		for n := range wantTools {
			enabledNames = append(enabledNames, n)
		}
		childReg := tools.NewRegistry(enabledNames)
		for _, pt := range parentTools {
			if wantTools[pt.Name()] {
				childReg.Register(pt)
			}
		}

		// Cap child budget by parent's remaining budget
		maxSteps := params.MaxSteps
		if maxSteps <= 0 {
			maxSteps = roleCfg.BudgetSteps
		}
		if maxSteps <= 0 {
			maxSteps = 10
		}
		if rem := budget.RemainingSteps(); rem > 0 && maxSteps > rem {
			maxSteps = rem
		}
		maxTokens := 25000
		if rem := budget.RemainingTokens(); rem > 0 && maxTokens > rem {
			maxTokens = rem
		}
		maxCost := 0.0
		if rem := budget.RemainingCost(); rem > 0 {
			maxCost = rem
		}

		childBudget := core.NewBudgetTracker(&core.Budget{
			MaxSteps:   maxSteps,
			MaxTokens:  maxTokens,
			MaxCostUSD: maxCost,
		})

		callShort := params.CallID
		if len(callShort) > 8 {
			callShort = callShort[:8]
		}
		childRunID := fmt.Sprintf("agent-%s", callShort)
		childRun := &core.Run{
			ID:  childRunID,
			Dir: workspace,
		}

		systemPrompt := strings.TrimSpace(roleCfg.SystemPrompt)
		if systemPrompt == "" {
			systemPrompt = "You are a focused sub-agent. Complete the given task concisely. Use the tools available to you."
		}
		policyName := "sub-agent"
		if strings.TrimSpace(params.Agent) != "" {
			policyName = "sub-agent:" + params.Agent
		}
		childPolicy := &policy.Policy{
			Name:         policyName,
			SystemPrompt: systemPrompt,
		}
		childPolicy.MaxToolCallsPerStep = parentMaxToolCalls
		if childPolicy.MaxToolCallsPerStep <= 0 {
			childPolicy.MaxToolCallsPerStep = cfg.Defaults.MaxToolCallsPerStep
		}
		if childPolicy.MaxToolCallsPerStep <= 0 {
			childPolicy.MaxToolCallsPerStep = 50
		}

		// Resolve output function
		var childOutputFn core.OutputFn
		if outputFn != nil {
			childOutputFn = *outputFn
		}

		// Emit agent.start event
		modelName := params.Model
		if modelName == "" {
			modelName = modelOverride
		}
		if modelName == "" {
			modelName = prov.Name()
		}
		startPayload := core.AgentStartPayload{
			Agent:        params.Agent,
			ParentCallID: params.CallID,
			AgentRunID:   childRunID,
			Task:         params.Task,
			Model:        modelName,
			Tools:        childReg.List(),
			MaxSteps:     maxSteps,
		}
		emitAgentEvent(trace, childOutputFn, params.RunID, params.StepID,
			params.CallID+"-astart", core.EventAgentStart, startPayload)

		childLoop := &core.Loop{
			Run:       childRun,
			Provider:  prov,
			Tools:     childReg,
			Policy:    childPolicy,
			Trace:     trace,
			Budget:    childBudget,
			ConfirmFn: confirmFn,
			OutputFn:  childOutputFn,
		}

		var result string
		var lastErr error
		ok := true
		taskPrompt := buildSubAgentTask(params.Task, "", 1)
		if stepErr := childLoop.Step(ctx, taskPrompt); stepErr != nil {
			lastErr = stepErr
		}
		result = extractLastAssistantText(childLoop.Messages)

		if !isCompliantAgentHandoff(result) && childBudget.RemainingSteps() != 0 {
			retryPrompt := buildSubAgentTask(params.Task, result, 2)
			if stepErr := childLoop.Step(ctx, retryPrompt); stepErr != nil {
				lastErr = stepErr
			}
			result = extractLastAssistantText(childLoop.Messages)
		}

		if !isCompliantAgentHandoff(result) {
			ok = false
			if lastErr != nil {
				result = fmt.Sprintf("sub-agent failed to produce a compliant handoff after 2 attempts: %v", lastErr)
			} else {
				preview := strings.TrimSpace(result)
				if len(preview) > 240 {
					preview = preview[:240] + "…"
				}
				if preview == "" {
					preview = "(empty)"
				}
				result = "sub-agent failed to produce a compliant handoff after 2 attempts; partial output: " + preview
			}
		}

		// Add child's consumed budget to parent
		cb := childBudget.Budget()
		_ = budget.AddTokens(cb.UsedTokens, 0)
		_ = budget.AddCost(cb.UsedCostUSD)

		// Emit agent.end event
		endPayload := core.AgentEndPayload{
			Agent:        params.Agent,
			ParentCallID: params.CallID,
			AgentRunID:   childRunID,
			OK:           ok,
			Result:       result,
			UsedSteps:    cb.UsedSteps,
			UsedTokens:   cb.UsedTokens,
			CostUSD:      cb.UsedCostUSD,
		}
		emitAgentEvent(trace, childOutputFn, params.RunID, params.StepID,
			params.CallID+"-aend", core.EventAgentEnd, endPayload)

		return tools.AgentRunResult{
			OK:         ok,
			Result:     result,
			UsedSteps:  cb.UsedSteps,
			UsedTokens: cb.UsedTokens,
			CostUSD:    cb.UsedCostUSD,
		}
	}

	reg.Register(tools.NewAgent(runFn))
	reg.Register(tools.NewDispatch(runFn, func() []string {
		names := make([]string, 0, len(cfg.Agents))
		for k := range cfg.Agents {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}))
}

func buildSubAgentTask(task, priorOutput string, attempt int) string {
	base := strings.TrimSpace(task)
	if base == "" {
		base = "(no task provided)"
	}
	contract := `
Return a final handoff with this exact structure:
## Summary
<2-4 sentences>

## Findings
- [P1|P2|P3] <issue> — <why it matters> — <file refs if available>
- [P1|P2|P3] ...

## Next Steps
1. <first action>
2. <second action>

Rules:
- Never return an empty response.
- If tools fail, still return the handoff and explain what failed.
- Keep total length under 350 words.
`
	if attempt <= 1 {
		return base + "\n\n" + strings.TrimSpace(contract)
	}
	return base + "\n\nYour previous response was not compliant or empty.\nPrevious output:\n" +
		strings.TrimSpace(priorOutput) + "\n\n" + strings.TrimSpace(contract)
}

func extractLastAssistantText(msgs []providers.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			v := strings.TrimSpace(msgs[i].Content)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func isCompliantAgentHandoff(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if len(s) < 80 {
		return false
	}
	return strings.Contains(s, "## Summary") &&
		strings.Contains(s, "## Findings") &&
		strings.Contains(s, "## Next Steps")
}

func emitAgentEvent(trace *core.TraceWriter, outputFn core.OutputFn,
	runID, stepID, eventID string, eventType core.EventType, payload any) {
	b, _ := json.Marshal(payload)
	ev := core.Event{
		TS:      time.Now().UTC(),
		RunID:   runID,
		StepID:  stepID,
		EventID: eventID,
		Type:    eventType,
		Payload: b,
	}
	if trace != nil {
		_ = trace.Write(ev)
	}
	if outputFn != nil {
		outputFn(ev)
	}
}

func loadPolicy(cfg *config.Config, name string) *policy.Policy {
	if name == "" {
		name = "default"
	}
	pc, ok := cfg.Policies[name]
	if !ok {
		return policy.Default()
	}
	p, err := policy.Load(name, pc)
	if err != nil {
		return policy.Default()
	}
	return p
}

func buildConfirmFn(mode string) core.ConfirmFn {
	switch mode {
	case "always":
		return ui.ConfirmTool
	case "never":
		return func(_, _ string) bool { return true }
	default: // "dangerous"
		return ui.ConfirmTool
	}
}

func reconstructHistory(events []core.Event) ([]providers.Message, string, string, string) {
	var msgs []providers.Message
	var providerName, model, workspace string

	for _, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			providerName = p.Provider
			model = p.Model
			workspace = strings.TrimSpace(p.Workspace)

		case core.EventUserMsg:
			var p core.UserMsgPayload
			_ = json.Unmarshal(ev.Payload, &p)
			msgs = append(msgs, providers.Message{Role: "user", Content: p.Content})

		case core.EventModelResp:
			var p core.ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			msgs = append(msgs, providers.Message{Role: "assistant", Content: p.Text})

		case core.EventToolResult:
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			content := p.Output
			if !p.OK {
				content = "ERROR: " + p.Output
			}
			msgs = append(msgs, providers.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: p.CallID,
				Name:       p.Name,
			})
		}
	}
	return msgs, providerName, model, workspace
}

func resolveWorkspace(workspaceFlag, runDir string) string {
	workspace := strings.TrimSpace(workspaceFlag)
	if workspace == "" {
		// Default to caller CWD so the agent operates on the project by default.
		if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
			workspace = wd
		} else {
			workspace = runDir
		}
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		return abs
	}
	return workspace
}

func findRunDir(runID string) (string, error) {
	// Try runs/<runID> first
	candidate := filepath.Join("runs", runID)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Try exact path
	if _, err := os.Stat(runID); err == nil {
		return runID, nil
	}
	return "", fmt.Errorf("run %q not found (checked runs/%s)", runID, runID)
}

func findInPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

// ─────────────────────────────────────────
// score command
// ─────────────────────────────────────────

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

// ─────────────────────────────────────────
// stats command
// ─────────────────────────────────────────

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
			}
			fmt.Print(core.FormatStats(stats))
			return nil
		},
	}
}

// ─────────────────────────────────────────
// metrics command
// ─────────────────────────────────────────

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

// ─────────────────────────────────────────
// compare command
// ─────────────────────────────────────────

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
				}
				allStats = append(allStats, s)
			}
			fmt.Print(core.FormatCompare(allStats))
			return nil
		},
	}
}

// ─────────────────────────────────────────
// bench command
// ─────────────────────────────────────────

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

			var allStats []core.RunStats

			for _, variant := range bc.Variants {
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
						RunID:    runID,
						Name:     bc.Name,
						Tags:     map[string]string{"experiment": bc.Name, "variant": variant.Name},
						Provider: variant.Provider,
						Model:    variant.Model,
						CreatedAt: time.Now().UTC(),
					}
					_ = core.WriteMeta(runDir, meta)

					tracePath := filepath.Join(runDir, "trace.jsonl")
					trace, err := core.OpenTrace(tracePath)
					if err != nil {
						return err
					}

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

					reg := buildToolRegistry(cfg)
					pol := loadPolicy(cfg, "default")

					budgetSteps := variant.BudgetSteps
					if budgetSteps == 0 {
						budgetSteps = cfg.Defaults.BudgetSteps
					}
					budget := core.NewBudgetTracker(&core.Budget{
						MaxSteps:   budgetSteps,
						MaxTokens:  cfg.Defaults.BudgetTokens,
						MaxCostUSD: cfg.Defaults.BudgetCostUSD,
					})

					run := &core.Run{ID: runID, Dir: runDir, TraceFile: tracePath}
					renderer := ui.NewCLIRenderer()
					confirmFn := func(_, _ string) bool { return true } // auto-approve
					outputFn := core.OutputFn(renderer.RenderEvent)

					loop := &core.Loop{
						Run:       run,
						Provider:  prov,
						Tools:     reg,
						Policy:    pol,
						Trace:     trace,
						Budget:    budget,
						ConfirmFn: confirmFn,
						OutputFn:  outputFn,
					}

					_ = loop.EmitRunStart(core.RunStartPayload{
						Policy:   pol.Name,
						Provider: prov.Name(),
						Model:    variant.Model,
					})

					ctx := context.Background()
					reason := "completed"
					if err := loop.Step(ctx, prompt.Message); err != nil {
						reason = "error"
					}
					_ = loop.EmitRunEnd(reason)
					trace.Close()

					// Compute stats
					events, _ := core.ReadAll(tracePath)
					s := core.ComputeStats(events)
					s.Score = meta.Score
					allStats = append(allStats, s)
				}
			}

			fmt.Printf("\n%s\n", ui.Header("Bench Results"))
			fmt.Print(core.FormatCompare(allStats))
			return nil
		},
	}
}

// ─────────────────────────────────────────
// query command
// ─────────────────────────────────────────

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
				fmt.Printf("%-28s  %-10s %-8s %-12s %s\n",
					meta.RunID, meta.Provider, meta.Model, score, meta.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&tagFilter, "tag", nil, "filter by tag key=value (repeatable)")
	cmd.Flags().StringVar(&scoreFilter, "score", "", "filter by score (pass|fail|partial)")
	return cmd
}

func parseTags(raw []string) map[string]string {
	tags := make(map[string]string)
	for _, s := range raw {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			tags[parts[0]] = parts[1]
		}
	}
	return tags
}

func newRunID() string {
	// Simple time-based ID
	return fmt.Sprintf("%s-%x", time.Now().UTC().Format("20060102T150405"), randBytes(4))
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	// Use crypto/rand via os
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return b
	}
	defer f.Close()
	_, _ = f.Read(b)
	return b
}

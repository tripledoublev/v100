package main

import (
	"context"
	"errors"
	"fmt"
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
		verboseFlag      bool
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

			// Ensure the active provider exists in config so we can apply overrides
			if _, ok := cfg.Providers[cfg.Defaults.Provider]; !ok {
				// Initialize from defaults if missing
				defaults := config.DefaultConfig()
				if pc, ok := defaults.Providers[cfg.Defaults.Provider]; ok {
					cfg.Providers[cfg.Defaults.Provider] = pc
				} else {
					// Create a generic entry if totally unknown
					cfg.Providers[cfg.Defaults.Provider] = config.ProviderConfig{
						Type: cfg.Defaults.Provider,
					}
				}
			}

			// Apply overrides to the ACTIVE provider
			if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
				if authFlag == "env" {
					// --auth with no value: load from .env / environment
					pc.Auth.Username = os.Getenv("OLLAMA_USER")
					pc.Auth.Password = os.Getenv("OLLAMA_PASS")
					if !cmd.Flags().Changed("base-url") {
						if envURL := os.Getenv("OLLAMA_BASE_URL"); envURL != "" {
							pc.BaseURL = envURL
						}
					}
				} else if authFlag != "" {
					parts := strings.SplitN(authFlag, ":", 2)
					if len(parts) == 2 {
						pc.Auth.Username = parts[0]
						pc.Auth.Password = parts[1]
					}
				}
				if baseURLFlag != "" {
					pc.BaseURL = baseURLFlag
				}
				if modelFlag != "" {
					pc.DefaultModel = modelFlag
				}
				cfg.Providers[cfg.Defaults.Provider] = pc
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
				Sandbox:         cfg.Sandbox,
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
			if err := reg.Validate(); err != nil {
				return fmt.Errorf("tool registry: %w", err)
			}

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
			if err := validateExecutionSafety(cfg, confirmMode, unsafeFlag); err != nil {
				return err
			}

			// Build generation params from flags and config defaults
			genParams := buildGenParams(cfg, temperatureFlag, topPFlag, topKFlag, maxTokensFlag, seedFlag, cmd)

			// Build solver
			solver, err := buildSolver(cfg, solverFlag)
			if err != nil {
				return err
			}

			if tuiFlag {
				return runWithTUI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, !tuiNoAltFlag, tuiPlainFlag, tuiDebugFlag, verboseFlag, genParams, solver, strings.Join(args, " "), session, mapper)
			}
			return runWithCLI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, verboseFlag, genParams, solver, strings.Join(args, " "), session, mapper)
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
	cmd.Flags().BoolVarP(&verboseFlag, "verbose", "v", false, "show full tool call details and verbose output")
	cmd.Flags().StringVar(&nameFlag, "name", "", "human-readable run name (stored in meta.json)")
	cmd.Flags().StringSliceVar(&tagFlags, "tag", nil, "key=value tags for the run (repeatable)")
	cmd.Flags().StringVar(&solverFlag, "solver", "", "solver type: react|plan_execute|router (default: react)")
	cmd.Flags().StringVar(&authFlag, "auth", "", "basic auth (user:pass); bare --auth loads from .env")
	cmd.Flags().Lookup("auth").NoOptDefVal = "env"
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
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, verbose bool, genParams providers.GenParams, solver core.Solver, initialPrompt string, session executor.Session, mapper *core.PathMapper) error {

	renderer := ui.NewCLIRenderer()
	renderer.Verbose = verbose

	confirmFn := buildConfirmFn(confirmMode)

	outputFn := core.OutputFn(renderer.RenderEvent)
	registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, workspace, pol.MaxToolCallsPerStep, session, mapper)

	loop := &core.Loop{
		Run:              run,
		Provider:         prov,
		CompressProvider: buildCompressProvider(cfg),
		Tools:            reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		ConfirmFn:        confirmFn,
		OutputFn:         outputFn,
		GenParams:        genParams,
		Solver:           solver,
		Session:          session,
		Mapper:           mapper,
		NetworkTier:      loopNetworkTier(cfg),
		Snapshots:        buildSnapshotManager(cfg, workspace),
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
	fmt.Println(ui.Info(ui.Dim("tools: ") + enabledToolSummary(reg)))
	fmt.Println(ui.Dim("Ctrl+C or /quit to exit"))

	reason := "user_exit"

	if initialPrompt != "" {
		if err := loop.Step(ctx, initialPrompt); err != nil {
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				fmt.Fprintln(os.Stderr, ui.Warn("budget exceeded: "+budgetErr.Reason))
				reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
				_ = loop.EmitRunEnd(reason, "")
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

	// Generate summary if possible
	finalSummary := ""
	if len(loop.Messages) > 1 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sumProv, _ := buildProvider(cfg, "gemini")
		if sumProv != nil {
			sumReq := providers.CompleteRequest{
				Model: "gemini-2.5-flash",
				Messages: append(loop.Messages, providers.Message{
					Role:    "user",
					Content: "Briefly summarize the outcome of this run in one sentence (max 20 words). What was achieved?",
				}),
			}
			if resp, err := sumProv.Complete(ctx, sumReq); err == nil {
				finalSummary = strings.TrimSpace(resp.AssistantText)
			}
		}
	}

	_ = loop.EmitRunEnd(reason, finalSummary)

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		fmt.Fprintln(os.Stderr, ui.Warn("sandbox finalize: "+err.Error()))
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}

	fmt.Println(ui.Dim("budget: " + budget.Summary()))
	return nil
}

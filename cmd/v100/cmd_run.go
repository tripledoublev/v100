package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
		yoloFlag         bool
		sandboxFlag      bool
		streamingFlag    bool
		budgetStepsFlag  int
		budgetTokensFlag int
		budgetCostFlag   float64
		toolTimeoutFlag  int
		maxToolCallsFlag int
		disableWatchdogs bool
		confirmToolsFlag string
		maxReplansFlag   int
		memoryModeFlag   string
		memoryTokensFlag int
		tuiFlag          bool
		tuiNoAltFlag     bool
		tuiPlainFlag     bool
		tuiDebugFlag     bool
		verboseFlag      bool
		exitFlag         bool
		planFlag         bool
		nameFlag         string
		tagFlags         []string
		solverFlag       string
		promptFileFlag   string
		authFlag         string
		baseURLFlag      string
		temperatureFlag  float64
		topPFlag         float64
		topKFlag         int
		maxTokensFlag    int
		seedFlag         int
	)

	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Start an interactive agent run",
		SilenceUsage: true,
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
			if err := validatePlanMode(planFlag, tuiFlag, solverFlag); err != nil {
				return err
			}
			if planFlag {
				cfg.Defaults.Solver = "plan_execute"
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
			if memoryModeFlag != "" {
				cfg.Defaults.MemoryMode = memoryModeFlag
			}
			if memoryTokensFlag > 0 {
				cfg.Defaults.MemoryMaxTokens = memoryTokensFlag
			}

			initialPrompt, err := resolveInitialPrompt(args, promptFileFlag)
			if err != nil {
				return err
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

			if cfg.Sandbox.Enabled {
				if fp, err := core.WorkspaceFingerprint(sourceWorkspace); err == nil {
					meta.SourceFingerprint = fp
				}
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
			if err := validateToolRegistry(reg); err != nil {
				return err
			}

			// Load policy
			pol := loadPolicy(cfg, policyFlag)
			if toolTimeoutFlag > 0 {
				pol.ToolTimeoutMS = toolTimeoutFlag
			}
			if disableWatchdogs {
				pol.DisableWatchdogs = true
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
			if yoloFlag {
				autoFlag = true
				unsafeFlag = true
			}
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
				return runWithTUI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, !tuiNoAltFlag, tuiPlainFlag, tuiDebugFlag, verboseFlag, genParams, solver, initialPrompt, session, mapper)
			}
			return runWithCLI(cfg, run, prov, reg, pol, trace, budget, model, confirmMode, workspace, verboseFlag, exitFlag, planFlag, genParams, solver, initialPrompt, session, mapper)
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider name (overrides config)")
	cmd.Flags().StringVar(&modelFlag, "model", "", "model name (overrides config)")
	cmd.Flags().StringVar(&policyFlag, "policy", "default", "policy name")
	cmd.Flags().StringVar(&runDirFlag, "run-dir", "", "base directory for runs (default: ./runs)")
	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "workspace directory for tool operations")
	cmd.Flags().BoolVar(&unsafeFlag, "unsafe", false, "disable path guardrails and confirmations")
	cmd.Flags().BoolVar(&autoFlag, "auto", false, "auto-approve all tool calls (no confirmation)")
	cmd.Flags().BoolVar(&yoloFlag, "yolo", false, "shorthand for --auto --unsafe (researcher mode)")
	cmd.Flags().BoolVar(&sandboxFlag, "sandbox", false, "enable isolated sandbox execution")
	cmd.Flags().BoolVar(&streamingFlag, "streaming", true, "enable real-time token streaming (disable with --streaming=false)")
	cmd.Flags().IntVar(&budgetStepsFlag, "budget-steps", 0, "max steps (0=config default)")
	cmd.Flags().IntVar(&budgetTokensFlag, "budget-tokens", 0, "max tokens (0=config default)")
	cmd.Flags().Float64Var(&budgetCostFlag, "budget-cost", 0, "max cost in USD (0=config default)")
	cmd.Flags().IntVar(&toolTimeoutFlag, "tool-timeout", 0, "tool timeout in ms (0=config default)")
	cmd.Flags().IntVar(&maxToolCallsFlag, "max-tool-calls-per-step", 0, "max tool calls per step")
	cmd.Flags().BoolVar(&disableWatchdogs, "disable-watchdogs", false, "disable inspection/read-heavy watchdog interventions")
	cmd.Flags().StringVar(&confirmToolsFlag, "confirm-tools", "", "confirm mode: always|dangerous|never")
	cmd.Flags().StringVar(&memoryModeFlag, "memory-mode", "", "memory injection mode: always|auto|off")
	cmd.Flags().IntVar(&memoryTokensFlag, "memory-max-tokens", 0, "approximate token budget for injected MEMORY.md context")
	cmd.Flags().BoolVar(&tuiFlag, "tui", false, "enable Bubble Tea TUI")
	cmd.Flags().BoolVar(&tuiNoAltFlag, "tui-no-alt", false, "disable alternate screen mode in TUI (for terminal compatibility)")
	cmd.Flags().BoolVar(&tuiPlainFlag, "tui-plain", false, "force plain monochrome TUI rendering for terminal compatibility")
	cmd.Flags().BoolVar(&tuiDebugFlag, "tui-debug", false, "write TUI startup/runtime debug log to run directory")
	cmd.Flags().BoolVarP(&verboseFlag, "verbose", "v", false, "show full tool call details and verbose output")
	cmd.Flags().BoolVar(&exitFlag, "exit", false, "exit after the initial prompt completes without entering interactive mode")
	cmd.Flags().BoolVar(&planFlag, "plan", false, "preview a plan and require approval before execution (CLI only)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "human-readable run name (stored in meta.json)")
	cmd.Flags().StringSliceVar(&tagFlags, "tag", nil, "key=value tags for the run (repeatable)")
	cmd.Flags().StringVar(&solverFlag, "solver", "", "solver type: react|plan_execute|router (default: react)")
	cmd.Flags().StringVar(&promptFileFlag, "prompt-file", "", "read the initial prompt from a file ('-' for stdin)")
	cmd.Flags().StringVar(&authFlag, "auth", "", "basic auth (user:pass); bare --auth loads from .env")
	cmd.Flags().Lookup("auth").NoOptDefVal = "env"
	cmd.Flags().StringVar(&baseURLFlag, "base-url", "", "override the provider's base API URL")
	cmd.Flags().IntVar(&maxReplansFlag, "max-replans", 0, "max replans for plan_execute solver")
	cmd.Flags().Float64Var(&temperatureFlag, "temperature", 0, "sampling temperature (0=provider default)")
	cmd.Flags().Float64Var(&topPFlag, "top-p", 0, "nucleus sampling top-p (0=provider default)")
	cmd.Flags().IntVar(&topKFlag, "top-k", 0, "top-k sampling (0=provider default)")
	cmd.Flags().IntVar(&maxTokensFlag, "max-tokens", 0, "max output tokens (0=provider default)")
	cmd.Flags().IntVar(&seedFlag, "seed", 0, "random seed for reproducibility (0=none)")
	_ = cmd.Flags().MarkHidden("disable-watchdogs")

	return cmd
}

func resolveInitialPrompt(args []string, promptFile string) (string, error) {
	if promptFile != "" && len(args) > 0 {
		return "", errors.New("initial prompt is ambiguous: use either trailing prompt args or --prompt-file")
	}
	if promptFile == "" {
		return strings.Join(args, " "), nil
	}
	if promptFile == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file %q: %w", promptFile, err)
	}
	return string(data), nil
}

func validatePlanMode(planMode bool, tuiMode bool, solverName string) error {
	if !planMode {
		return nil
	}
	if solverName != "" && solverName != "plan_execute" {
		return fmt.Errorf("--plan requires --solver plan_execute (got %q)", solverName)
	}
	if tuiMode {
		return fmt.Errorf("--plan is currently supported in CLI mode only")
	}
	return nil
}

func runWithCLI(cfg *config.Config, run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string, verbose bool, exitAfterPrompt bool, planMode bool, genParams providers.GenParams, solver core.Solver, initialPrompt string, session executor.Session, mapper *core.PathMapper) error {

	// Auto-exit when stdin is piped (not a TTY) to avoid hanging after prompt
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		exitAfterPrompt = true
	}

	renderer := ui.NewCLIRenderer()
	renderer.Verbose = verbose

	baseConfirmFn := buildConfirmFn(confirmMode)
	var confirmActive atomic.Bool
	confirmFn := wrapConfirmFnWithActivity(baseConfirmFn, &confirmActive)

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

	reason := "user_exit"

	// Step-level cancellation
	var stepCancel context.CancelFunc
	var stepMu sync.Mutex

	// Signal handler
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range sigs {
			stepMu.Lock()
			if stepCancel != nil {
				fmt.Fprintln(os.Stderr, ui.Warn("\ninterrupted by signal"))
				stepCancel()
				stepCancel = nil
				stepMu.Unlock()
				continue
			}
			stepMu.Unlock()
			reason = "user_exit"
			_ = loop.EmitRunEnd(reason, "")
			os.Exit(0)
		}
	}()

	// Escape key listener (only when terminal)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		go func() {
			// This goroutine runs for the life of the run
			for {
				// We only put it in raw mode briefly when we want to check for Escape
				// but wait, we need to read it while the agent is busy.
				// If the agent is busy, it's not reading from Stdin.
				// So we can put it in raw mode in a loop and read bytes.

				// However, if we are in a prompt, ui.Prompt is reading.
				// So we should only do this when stepCancel != nil.

				time.Sleep(100 * time.Millisecond)
				stepMu.Lock()
				busy := stepCancel != nil
				stepMu.Unlock()

				if busy && !confirmActive.Load() {
					oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
					if err == nil {
						// Read one byte
						var b [1]byte
						_, _ = os.Stdin.Read(b[:])
						_ = term.Restore(int(os.Stdin.Fd()), oldState)

						if b[0] == 27 { // Escape
							stepMu.Lock()
							if stepCancel != nil {
								fmt.Fprintln(os.Stderr, ui.Warn("\ninterrupted by Escape"))
								stepCancel()
								stepCancel = nil
							}
							stepMu.Unlock()
						}
					}
				}
			}
		}()
	}

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
	fmt.Println(ui.Info(ui.Dim("tools: ") + enabledToolSummaryVerbose(reg, verbose)))
	{
		solverName := solverDisplayName(solver)
		policyName := pol.Name
		if policyName == "" {
			policyName = "default"
		}
		compressInfo := ui.Dim("harness: ") + solverName + "  " + ui.Dim("policy: ") + policyName
		if cp := cfg.Defaults.CompressProvider; cp != "" {
			compressInfo += "  " + ui.Dim("compress: ") + cp
		}
		fmt.Println(ui.Info(compressInfo))
		if planMode {
			fmt.Println(ui.Info(ui.Dim("plan mode: preview + approval")))
		}
		if verbose {
			fmt.Println(ui.Info(ui.Dim("entrypoint: cmd/v100  runtime: internal/core (" + solverName + " loop)")))
		}
	}
	fmt.Println(ui.Dim("Ctrl+C or /quit to exit"))

	var providerErr bool

	if initialPrompt != "" {
		stepMu.Lock()
		var stepCtx context.Context
		stepCtx, stepCancel = context.WithCancel(ctx)
		stepMu.Unlock()

		err := runCLIInput(stepCtx, loop, initialPrompt, nil, planMode)

		stepMu.Lock()
		stepCancel = nil
		stepMu.Unlock()

		if err != nil {
			if errors.Is(err, context.Canceled) {
				// User interrupted intentionally; don't emit error event
			} else {
				var budgetErr *core.ErrBudgetExceeded
				if errors.As(err, &budgetErr) {
					fmt.Fprintln(os.Stderr, ui.Warn("budget exceeded: "+budgetErr.Reason))
					reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
					_ = loop.EmitRunEnd(reason, "")
					return nil
				}
				var retryErr *providers.RetryableError
				if errors.As(err, &retryErr) {
					providerErr = true
					fmt.Fprintln(os.Stderr, ui.Fail("provider error: "+formatRetryError(retryErr)))
				} else {
					fmt.Fprintln(os.Stderr, ui.Fail("initial step error: "+err.Error()))
				}
			}
		}
		if exitAfterPrompt {
			reason = "prompt_exit"
			goto done
		}
	}

	for {
		promptResult, err := ui.PromptWithImages("")
		if err != nil {
			reason = "user_exit"
			break
		}
		input := strings.TrimSpace(promptResult.Text)
		images := make([]providers.ImageAttachment, 0, len(promptResult.Images))
		for _, img := range promptResult.Images {
			images = append(images, providers.ImageAttachment{MIMEType: "image/png", Data: img})
		}
		if input == "" && len(images) == 0 {
			continue
		}
		if input == "/quit" || input == "/exit" {
			break
		}

		stepMu.Lock()
		var stepCtx context.Context
		stepCtx, stepCancel = context.WithCancel(ctx)
		stepMu.Unlock()

		err = runCLIInput(stepCtx, loop, input, images, planMode)

		stepMu.Lock()
		stepCancel = nil
		stepMu.Unlock()

		if err != nil {
			if errors.Is(err, context.Canceled) {
				// User interrupted intentionally; don't emit error event
			} else {
				var budgetErr *core.ErrBudgetExceeded
				if errors.As(err, &budgetErr) {
					fmt.Fprintln(os.Stderr, ui.Warn("budget exceeded: "+budgetErr.Reason))
					reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
					break
				}
				var retryErr *providers.RetryableError
				if errors.As(err, &retryErr) {
					providerErr = true
					fmt.Fprintln(os.Stderr, ui.Fail("provider error: "+formatRetryError(retryErr)))
				} else {
					fmt.Fprintln(os.Stderr, ui.Fail("step error: "+err.Error()))
				}
			}
		}
	}

done:
	var finalSummary string
	// Only generate summary for actual errors, not user_exit
	if !providerErr && reason != "user_exit" {
		finalSummary = generateRunSummary(context.Background(), prov, model, loop.Messages)
	}

	_ = loop.EmitRunEnd(reason, finalSummary)

	if result, err := finalizeSandboxRun(cfg, run, reason, mapper); err != nil {
		fmt.Fprintln(os.Stderr, ui.Warn("sandbox finalize: "+err.Error()))
	} else if result != nil {
		fmt.Println(ui.Info(sandboxFinalizeMessage(*result)))
	}
	maybePrintFailureDigest(os.Stderr, trace.Path(), reason)

	fmt.Println(ui.Dim("budget: " + budget.Summary()))
	fmt.Println(ui.Dim("run id: ") + run.ID)
	fmt.Println(ui.Dim("  → v100 stats " + run.ID))
	return nil
}

func wrapConfirmFnWithActivity(confirmFn core.ConfirmFn, active *atomic.Bool) core.ConfirmFn {
	if confirmFn == nil {
		return nil
	}
	return func(toolName, args string) bool {
		active.Store(true)
		defer active.Store(false)
		return confirmFn(toolName, args)
	}
}

func runCLIInput(ctx context.Context, loop *core.Loop, input string, images []providers.ImageAttachment, planMode bool) error {
	if !planMode {
		return loop.StepWithImages(ctx, input, images)
	}
	if len(images) > 0 {
		return fmt.Errorf("plan mode does not support image attachments yet")
	}
	planSolver, ok := loop.Solver.(*core.PlanExecuteSolver)
	if !ok {
		return fmt.Errorf("plan mode requires plan_execute solver")
	}
	plan, err := planSolver.Preview(ctx, loop, input)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(ui.Info(ui.Dim("plan preview")))
	fmt.Println(plan)
	if !confirmPlanExecution() {
		fmt.Println(ui.Warn("plan execution skipped"))
		return nil
	}
	_, err = planSolver.ExecuteApprovedPlan(ctx, loop, input, plan, loop.Budget.Budget())
	return err
}

func confirmPlanExecution() bool {
	fmt.Print(ui.Warn("Execute this plan? [y/N] "))
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	return isApprovedPlanAnswer(scanner.Text())
}

func isApprovedPlanAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// formatRetryError formats a RetryableError with reset time for user clarity.
func formatRetryError(retryErr *providers.RetryableError) string {
	msg := retryErr.Err.Error()
	if retryErr.RetryAfter > 0 {
		resetTime := retryErr.RetryAfter.Round(time.Second)
		msg += fmt.Sprintf(" — quota reset after %s", resetTime.String())
	}
	return msg
}

// generateRunSummary generates a one-sentence summary of a completed run.
// Fix #9: Uses the run's own provider (not a hardcoded Gemini) and
// injects a system message so the model knows it's summarizing a completed run.
func generateRunSummary(ctx context.Context, prov providers.Provider, model string, messages []providers.Message) string {
	if len(messages) <= 1 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Pass only last 20 messages to avoid hitting token limits
	msgs := messages
	const maxSummaryMsgs = 20
	if len(msgs) > maxSummaryMsgs {
		msgs = msgs[len(msgs)-maxSummaryMsgs:]
	}

	// Prepend a system-level context message so the model knows its role
	contextMsg := providers.Message{
		Role:    "user",
		Content: "The following is a completed agent run transcript. Your task is to summarize it.",
	}
	ackMsg := providers.Message{
		Role:    "assistant",
		Content: "Understood. I will summarize the completed run based on the transcript.",
	}
	summaryMsgs := append([]providers.Message{contextMsg, ackMsg}, msgs...)
	summaryMsgs = append(summaryMsgs, providers.Message{
		Role:    "user",
		Content: "Briefly summarize the outcome of this run in one sentence (max 20 words). What was achieved?",
	})

	req := providers.CompleteRequest{
		Model:    model,
		Messages: summaryMsgs,
	}
	if resp, err := prov.Complete(ctx, req); err == nil {
		return strings.TrimSpace(resp.AssistantText)
	}
	return ""
}

func blameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blame <run_id> <file>",
		Short: "Show which reasoning turn wrote to a file",
		Long:  "Inspect a file and see which event IDs (reasoning turns) modified it during the run.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			filePath := args[1]

			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}

			return showFileBlame(events, filePath)
		},
	}
	return cmd
}

func showFileBlame(events []core.Event, filePath string) error {
	// Normalize file path for comparison (handle both absolute and relative)
	filePath = strings.TrimSpace(filePath)
	fileBase := filepath.Base(filePath)

	type blameEntry struct {
		EventID      string
		CallID       string
		StepID       string
		ToolName     string
		WritePath    string
		Content      string
		BytesWritten int
	}

	// Build a map of CallID → fs_write call details by looking at tool.call events
	callDetails := make(map[string]struct {
		Path    string
		Content string
		Append  bool
	})

	for _, ev := range events {
		if ev.Type != core.EventToolCall {
			continue
		}

		var payload core.ToolCallPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}

		if payload.Name != "fs_write" {
			continue
		}

		// Parse the args to extract path and content
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Append  bool   `json:"append"`
		}
		if err := json.Unmarshal([]byte(payload.Args), &args); err != nil {
			continue
		}

		callDetails[payload.CallID] = struct {
			Path    string
			Content string
			Append  bool
		}{
			Path:    args.Path,
			Content: args.Content,
			Append:  args.Append,
		}
	}

	var blameEntries []blameEntry

	// Now match results to calls and filter by file
	for _, ev := range events {
		if ev.Type != core.EventToolResult {
			continue
		}

		var payload core.ToolResultPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}

		// Look for fs_write tool results
		if payload.Name != "fs_write" || !payload.OK {
			continue
		}

		// Get the call details
		details, ok := callDetails[payload.CallID]
		if !ok {
			continue
		}

		// Check if this write targets our file
		if !matchesFile(details.Path, filePath, fileBase) {
			continue
		}

		// Parse the output to get bytes written
		var writeOutput struct {
			BytesWritten int `json:"bytes_written,omitempty"`
		}
		_ = json.Unmarshal([]byte(payload.Output), &writeOutput)

		blameEntries = append(blameEntries, blameEntry{
			EventID:      ev.EventID,
			CallID:       payload.CallID,
			StepID:       ev.StepID,
			ToolName:     payload.Name,
			WritePath:    details.Path,
			Content:      details.Content,
			BytesWritten: writeOutput.BytesWritten,
		})
	}

	if len(blameEntries) == 0 {
		fmt.Printf("No writes found to file: %s\n", filePath)
		return nil
	}

	// Display results
	fmt.Printf("\n%s\n", ui.Header(fmt.Sprintf("File Provenance: %s", filePath)))
	fmt.Printf("Found %d write operation(s):\n\n", len(blameEntries))

	for i, entry := range blameEntries {
		fmt.Printf("[%d] Event: %s\n", i+1, ui.Bold(entry.EventID))
		fmt.Printf("    File:  %s\n", entry.WritePath)
		fmt.Printf("    Bytes: %d\n", entry.BytesWritten)
		if entry.Content != "" {
			preview := entry.Content
			if len(preview) > 100 {
				preview = preview[:100] + "…"
			}
			fmt.Printf("    Content: %s\n", strings.TrimSpace(preview))
		}
		fmt.Println()
	}

	return nil
}

// matchesFile checks if a written file path matches the target file
func matchesFile(writePath, targetPath, targetBase string) bool {
	writePath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(writePath)))
	targetPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(targetPath)))

	if writePath == targetPath {
		return true
	}

	if !strings.Contains(targetPath, "/") {
		return filepath.Base(writePath) == targetBase
	}

	if strings.HasSuffix(writePath, "/"+targetPath) {
		return true
	}

	return false
}

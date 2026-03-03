package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		Use:   "agent",
		Short: "Modular CLI/TUI agent harness",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: ~/.config/v100/config.toml)")

	root.AddCommand(
		runCmd(&cfgPath),
		resumeCmd(&cfgPath),
		replayCmd(),
		toolsCmd(&cfgPath),
		providersCmd(&cfgPath),
		configInitCmd(),
		doctorCmd(&cfgPath),
		loginCmd(),
		logoutCmd(),
	)
	return root
}

// ─────────────────────────────────────────
// agent run
// ─────────────────────────────────────────

func runCmd(cfgPath *string) *cobra.Command {
	var (
		providerFlag        string
		modelFlag           string
		policyFlag          string
		runDirFlag          string
		workspaceFlag       string
		unsafeFlag          bool
		autoFlag            bool
		budgetStepsFlag     int
		budgetTokensFlag    int
		budgetCostFlag      float64
		toolTimeoutFlag     int
		maxToolCallsFlag    int
		confirmToolsFlag    string
		tuiFlag             bool
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
			workspace := workspaceFlag
			if workspace == "" {
				workspace = runDir
			}

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
				return runWithTUI(run, prov, reg, pol, trace, budget, model, confirmMode, workspace)
			}
			return runWithCLI(run, prov, reg, pol, trace, budget, model, confirmMode, workspace)
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

	return cmd
}

func runWithCLI(run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string) error {

	renderer := ui.NewCLIRenderer()

	confirmFn := buildConfirmFn(confirmMode)

	loop := &core.Loop{
		Run:      run,
		Provider: prov,
		Tools:    reg,
		Policy:   pol,
		Trace:    trace,
		Budget:   budget,
		ConfirmFn: confirmFn,
		OutputFn:  renderer.RenderEvent,
	}

	// Override workspace for tool execution
	run.Dir = workspace

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:   pol.Name,
		Provider: prov.Name(),
		Model:    model,
	}); err != nil {
		return err
	}

	fmt.Printf("Agent run started. ID: %s\n", run.ID)
	fmt.Printf("Trace: %s\n", run.TraceFile)
	fmt.Printf("Budget: %s\n", budget.Summary())
	fmt.Println("Type your message and press Enter. Ctrl+C to quit.")

	ctx := context.Background()
	reason := "user_exit"

	for {
		input, err := ui.Prompt("> ")
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
				fmt.Fprintf(os.Stderr, "\nBudget exceeded: %s\n", budgetErr.Reason)
				reason = "budget_" + strings.SplitN(budgetErr.Reason, ":", 2)[0]
				break
			}
			fmt.Fprintf(os.Stderr, "Step error: %v\n", err)
		}
	}

	_ = loop.EmitRunEnd(reason)
	fmt.Printf("\nRun ended. Budget: %s\n", budget.Summary())
	return nil
}

func runWithTUI(run *core.Run, prov providers.Provider, reg *tools.Registry, pol *policy.Policy,
	trace *core.TraceWriter, budget *core.BudgetTracker, model, confirmMode, workspace string) error {

	run.Dir = workspace

	var tui *ui.TUI
	ctx := context.Background()
	reason := "user_exit"

	var loop *core.Loop

	submitFn := func(input string) {
		if err := loop.Step(ctx, input); err != nil {
			var budgetErr *core.ErrBudgetExceeded
			if errors.As(err, &budgetErr) {
				_ = loop.EmitRunEnd("budget_exceeded")
				tui.Quit()
			}
		}
	}

	tui = ui.NewTUI(submitFn)

	confirmFn := func(toolName, args string) bool {
		if confirmMode == "never" {
			return true
		}
		if confirmMode == "always" || (confirmMode == "dangerous" && reg.IsDangerous(toolName)) {
			return tui.RequestConfirm(toolName, args)
		}
		return true
	}

	loop = &core.Loop{
		Run:      run,
		Provider: prov,
		Tools:    reg,
		Policy:   pol,
		Trace:    trace,
		Budget:   budget,
		ConfirmFn: confirmFn,
		OutputFn:  func(ev core.Event) { tui.SendEvent(ev) },
	}

	if err := loop.EmitRunStart(core.RunStartPayload{
		Policy:   pol.Name,
		Provider: prov.Name(),
		Model:    model,
	}); err != nil {
		return err
	}

	if err := tui.Run(); err != nil {
		return err
	}

	_ = loop.EmitRunEnd(reason)
	return nil
}

// ─────────────────────────────────────────
// agent resume
// ─────────────────────────────────────────

func resumeCmd(cfgPath *string) *cobra.Command {
	var tuiFlag bool

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
			msgs, providerName, model := reconstructHistory(events)

			if providerName == "" {
				providerName = cfg.Defaults.Provider
			}

			prov, err := buildProvider(cfg, providerName)
			if err != nil {
				return err
			}

			reg := buildToolRegistry(cfg)
			pol := loadPolicy(cfg, "default")
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

			fmt.Printf("Resuming run %s (%d events loaded)\n", runID, len(events))
			_ = model
			_ = tuiFlag

			ctx := context.Background()
			reason := "user_exit"
			for {
				input, err := ui.Prompt("> ")
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
	return cmd
}

// ─────────────────────────────────────────
// agent replay
// ─────────────────────────────────────────

func replayCmd() *cobra.Command {
	return &cobra.Command{
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

			for _, ev := range events {
				ui.PrintReplayEvent(ev)
			}
			return nil
		},
	}
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
				fmt.Printf("Config already exists at %s\n", path)
				fmt.Print("Overwrite? [y/N] ")
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
			fmt.Printf("Config written to %s\n", path)

			// Also write default policy prompt
			if err := policy.WriteDefaultPrompt(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write default policy: %v\n", err)
			} else {
				home, _ := os.UserHomeDir()
				fmt.Printf("Default policy written to %s/.config/v100/policies/default.md\n", home)
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
			fmt.Println("Starting OAuth login flow...")
			t, err := auth.Login(ctx)
			if err != nil {
				return fmt.Errorf("login: %w", err)
			}
			path := auth.DefaultTokenPath()
			if err := auth.Save(path, t); err != nil {
				return fmt.Errorf("login: save token: %w", err)
			}
			fmt.Printf("Logged in successfully.\n")
			if t.AccountID != "" {
				fmt.Printf("Account ID: %s\n", t.AccountID)
			}
			fmt.Printf("Token saved to: %s\n", path)
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
					fmt.Println("Already logged out (no token found).")
					return nil
				}
				return fmt.Errorf("logout: %w", err)
			}
			fmt.Printf("Logged out. Token removed from %s\n", path)
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
			ok := true

			// 1. Config
			cfgFile := *cfgPath
			if cfgFile == "" {
				cfgFile = config.XDGConfigPath()
			}
			if _, err := os.Stat(cfgFile); err == nil {
				fmt.Printf("✓ Config: %s\n", cfgFile)
			} else {
				fmt.Printf("✗ Config not found at %s (run: agent config init)\n", cfgFile)
				ok = false
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				fmt.Printf("✗ Config parse error: %v\n", err)
				return nil
			}

			// 2. Provider auth
			for name, pc := range cfg.Providers {
				switch pc.Type {
				case "codex":
					tokenPath := auth.DefaultTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Printf("✓ Provider %s: auth token found at %s\n", name, tokenPath)
					} else {
						fmt.Printf("✗ Provider %s: auth token not found at %s — run 'agent login'\n", name, tokenPath)
						ok = false
					}
				default:
					key := os.Getenv(pc.Auth.Env)
					if key == "" {
						fmt.Printf("✗ Provider %s: env var %s not set\n", name, pc.Auth.Env)
						ok = false
					} else {
						fmt.Printf("✓ Provider %s: auth env %s set (%d chars)\n", name, pc.Auth.Env, len(key))
					}
				}
			}

			// 3. ripgrep
			{
				p, err := findInPath("rg")
				if err != nil || p == "" {
					fmt.Println("✗ rg (ripgrep) not found in PATH — project.search will fail")
					ok = false
				} else {
					fmt.Printf("✓ rg: %s\n", p)
				}
			}

			// 4. patch
			if p, _ := findInPath("patch"); p != "" {
				fmt.Printf("✓ patch: %s\n", p)
			} else {
				fmt.Println("✗ patch not found — patch.apply will fail")
				ok = false
			}

			// 5. git
			if p, _ := findInPath("git"); p != "" {
				fmt.Printf("✓ git: %s\n", p)
			} else {
				fmt.Println("✗ git not found — git tools will fail")
				ok = false
			}

			// 6. runs dir writable
			runsDir := "runs"
			if err := os.MkdirAll(runsDir, 0o755); err == nil {
				testFile := filepath.Join(runsDir, ".doctor_test")
				if f, err := os.Create(testFile); err == nil {
					f.Close()
					os.Remove(testFile)
					fmt.Printf("✓ runs/ dir writable\n")
				} else {
					fmt.Printf("✗ runs/ dir not writable: %v\n", err)
					ok = false
				}
			}

			if ok {
				fmt.Println("\nAll checks passed.")
			} else {
				fmt.Println("\nSome checks failed. Fix issues above before running.")
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
	reg.Register(tools.PatchApply())
	reg.Register(tools.ProjectSearch())
	return reg
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

func reconstructHistory(events []core.Event) ([]providers.Message, string, string) {
	var msgs []providers.Message
	var providerName, model string

	for _, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			providerName = p.Provider
			model = p.Model

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
	return msgs, providerName, model
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

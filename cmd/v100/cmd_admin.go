package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/auth"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

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
			if err := validateToolRegistry(reg); err != nil {
				return err
			}
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

func providersCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Providers))
			for name := range cfg.Providers {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				pc := cfg.Providers[name]
				pc = normalizedProviderConfig(pc)
				fbs := ""
				if len(pc.Fallbacks) > 0 {
					fbs = "  fallbacks=[" + strings.Join(pc.Fallbacks, ", ") + "]"
				}
				fmt.Printf("%-15s type=%-10s model=%s%s\n", name, pc.Type, pc.DefaultModel, fbs)
			}
			return nil
		},
	}
	cmd.AddCommand(providersHealthCmd(cfgPath))
	return cmd
}

func providersHealthCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Show health status of all configured providers (requires running a provider to populate health data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			var allStatuses []providers.HealthStatus
			names := make([]string, 0, len(cfg.Providers))
			for name := range cfg.Providers {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				prov, err := buildProvider(cfg, name)
				if err != nil {
					continue
				}
				if hr, ok := prov.(interface {
					HealthStatus() []providers.HealthStatus
				}); ok {
					allStatuses = append(allStatuses, hr.HealthStatus()...)
				}
			}
			if len(allStatuses) == 0 {
				fmt.Println("No health data yet. Health tracking populates as providers are used.")
				return nil
			}
			fmt.Print(providers.FormatHealthStatus(allStatuses))
			return nil
		},
	}
}

func agentsCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List configured agent roles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			names := configuredAgentNames(cfg)
			if len(names) == 0 {
				fmt.Println("No agent roles configured. Add [agents.<name>] blocks to your config.")
				return nil
			}
			for _, name := range names {
				agent := cfg.Agents[name]
				model := strings.TrimSpace(agent.Model)
				if model == "" {
					model = "(default)"
				}
				fmt.Printf("%-12s model=%-12s steps=%-3d tokens=%-6d tools=%s\n",
					name,
					model,
					agent.BudgetSteps,
					agent.BudgetTokens,
					strings.Join(agent.Tools, ","),
				)
			}
			return nil
		},
	}
	cmd.AddCommand(agentsRunCmd(cfgPath))
	return cmd
}

type agentRunOptions struct {
	Provider          string
	Model             string
	ToolsCSV          string
	MaxSteps          int
	Workspace         string
	HandoffSchemaName string
	HandoffSchemaFile string
	JSON              bool
}

func agentsRunCmd(cfgPath *string) *cobra.Command {
	var opts agentRunOptions
	cmd := &cobra.Command{
		Use:   "run <agent> <task...>",
		Short: "Run a configured agent role directly",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			role := strings.TrimSpace(args[0])
			task := strings.TrimSpace(strings.Join(args[1:], " "))
			return runAgentRoleCommand(cmd.Context(), cfgPath, role, task, opts)
		},
	}
	cmd.Flags().StringVar(&opts.Provider, "provider", "", "provider override for this agent run")
	cmd.Flags().StringVar(&opts.Model, "model", "", "model override for this agent run")
	cmd.Flags().StringVar(&opts.ToolsCSV, "tools", "", "comma-separated tool subset for this agent run")
	cmd.Flags().IntVar(&opts.MaxSteps, "max-steps", 0, "step limit override for this agent run")
	cmd.Flags().StringVar(&opts.Workspace, "workspace", "", "workspace directory for the agent run (default: current directory)")
	cmd.Flags().StringVar(&opts.HandoffSchemaName, "handoff-schema-name", tools.HandoffSchemaStandard, "named handoff schema to require")
	cmd.Flags().StringVar(&opts.HandoffSchemaFile, "handoff-schema-file", "", "custom JSON schema file for the final handoff")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "print the structured result JSON only")
	return cmd
}

func runAgentRoleCommand(ctx context.Context, cfgPath *string, role, task string, opts agentRunOptions) error {
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("agent role is required")
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("task is required")
	}
	if _, ok := cfg.Agents[role]; !ok {
		return fmt.Errorf("%s", formatUnknownAgentRole(cfg, role))
	}

	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		workspace = cwd
	}
	workspace = expandHomePath(workspace)
	opts.Workspace = workspace

	comp, err := BuildRunComponents(cfg, RunOptions{Workspace: workspace})
	if err != nil {
		return err
	}
	defer cleanupRunComponents(comp)

	renderer := ui.NewCLIRenderer()
	var outputFn core.OutputFn
	var outputFnPtr *core.OutputFn
	if !opts.JSON {
		outputFn = core.OutputFn(renderer.RenderEvent)
		outputFnPtr = &outputFn
	}
	confirmFn := buildConfirmFn(cfg.Defaults.ConfirmTools)
	registerAgentTool(cfg, comp.Registry, comp.Trace, comp.Budget, outputFnPtr, confirmFn, comp.Workspace, comp.Policy.MaxToolCallsPerStep, comp.Session, comp.Mapper, comp.ToolEnv, comp.RedactToolOutput)

	parentLoop := &core.Loop{
		Run:           comp.Run,
		Provider:      comp.Provider,
		Model:         comp.Model,
		Policy:        comp.Policy,
		Trace:         comp.Trace,
		Budget:        comp.Budget,
		OutputFn:      outputFn,
		ModelMetadata: comp.ModelMetadata,
	}
	if err := parentLoop.EmitRunStart(core.RunStartPayload{
		Policy:        comp.Policy.Name,
		Provider:      comp.Provider.Name(),
		Model:         comp.Model,
		Workspace:     traceWorkspace(cfg, comp.Workspace),
		ModelMetadata: comp.ModelMetadata,
	}); err != nil {
		return err
	}

	res, err := runAgentDispatchTool(ctx, comp, role, task, opts)
	endReason := "completed"
	if err != nil || !res.OK {
		endReason = "error"
	}
	_ = parentLoop.EmitRunEnd(endReason, "")
	if err != nil {
		return err
	}

	if opts.JSON {
		if len(res.Structured) > 0 && json.Valid(res.Structured) {
			fmt.Println(string(res.Structured))
		} else {
			raw, _ := json.Marshal(res)
			fmt.Println(string(raw))
		}
	} else {
		fmt.Print(res.Output)
		if !strings.HasSuffix(res.Output, "\n") {
			fmt.Println()
		}
		fmt.Println(ui.Dim("trace: ") + comp.Run.TraceFile)
	}
	if !res.OK {
		return fmt.Errorf("agent run failed")
	}
	return nil
}

func runAgentDispatchTool(ctx context.Context, comp *RunComponents, role, task string, opts agentRunOptions) (tools.ToolResult, error) {
	dispatchTool, ok := comp.Registry.Get("dispatch")
	if !ok {
		return tools.ToolResult{}, fmt.Errorf("dispatch tool not registered")
	}
	handoffSchema, err := readAgentRunHandoffSchema(opts.HandoffSchemaFile)
	if err != nil {
		return tools.ToolResult{}, err
	}
	payload := map[string]any{
		"agent":               role,
		"task":                task,
		"provider":            strings.TrimSpace(opts.Provider),
		"model":               strings.TrimSpace(opts.Model),
		"max_steps":           opts.MaxSteps,
		"handoff_schema_name": strings.TrimSpace(opts.HandoffSchemaName),
	}
	if toolsList := splitAgentRunCSV(opts.ToolsCSV); len(toolsList) > 0 {
		payload["tools"] = toolsList
	}
	if len(handoffSchema) > 0 {
		payload["handoff_schema"] = json.RawMessage(handoffSchema)
	}
	rawArgs, err := json.Marshal(payload)
	if err != nil {
		return tools.ToolResult{}, err
	}
	hostWorkspace := strings.TrimSpace(opts.Workspace)
	if hostWorkspace == "" {
		hostWorkspace = comp.Workspace
	}
	return dispatchTool.Exec(ctx, tools.ToolCallContext{
		RunID:            comp.Run.ID,
		StepID:           "operator",
		CallID:           fmt.Sprintf("operator-%d", time.Now().UnixNano()),
		WorkspaceDir:     comp.Workspace,
		HostWorkspaceDir: hostWorkspace,
		StateDir:         comp.Run.StateDir,
		Provider:         comp.Provider,
		EmbedProvider:    comp.EmbedProvider,
		Registry:         comp.Registry,
		Session:          comp.Session,
		Mapper:           comp.Mapper,
		Env:              append([]string(nil), comp.ToolEnv...),
		RedactText:       comp.RedactToolOutput,
	}, rawArgs)
}

func readAgentRunHandoffSchema(path string) (json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(expandHomePath(path))
	if err != nil {
		return nil, err
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if !json.Valid(raw) {
		return nil, fmt.Errorf("handoff schema file %s is not valid JSON", path)
	}
	return json.RawMessage(raw), nil
}

func splitAgentRunCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func configInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config init",
		Short: "Write default config template to XDG config path",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.XDGConfigPath()
			if _, err := os.Stat(path); err == nil {
				fmt.Println(ui.Warn("Config already exists at " + path))
				fmt.Print(ui.Dim("Overwrite? [y/N] "))
				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						return fmt.Errorf("read confirmation: %w", err)
					}
					return nil
				}
				ans := scanner.Text()
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

			credsPath := auth.DefaultCredentialsPath()
			switch _, err := os.Stat(credsPath); {
			case err == nil:
				fmt.Println(ui.Warn("Plaintext OAuth fallback exists at " + credsPath))
				fmt.Println(ui.Dim("Prefer env vars or 1Password/pass/system keyring for OAuth client secrets."))
			case os.IsNotExist(err):
				fmt.Println(ui.OK("OAuth client secrets not written to disk by default"))
				fmt.Println(ui.Dim("Set V100_CODEX_CLIENT_ID, V100_GEMINI_CLIENT_ID/V100_GEMINI_CLIENT_SECRET, or V100_MINIMAX_CLIENT_ID."))
				fmt.Println(ui.Dim("Secret manager keys: oauth_codex_client_id, oauth_gemini_client_id, oauth_gemini_client_secret, oauth_minimax_client_id."))
				fmt.Println(ui.Dim("Plaintext fallback remains supported with a warning at " + credsPath))
			default:
				return err
			}

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

func loginCmd() *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate via browser OAuth",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			switch provider {
			case "codex", "":
				fmt.Println(ui.Info("Starting OAuth login flow (ChatGPT Plus/Pro)…"))
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

			case "gemini":
				fmt.Println(ui.Info("Starting OAuth login flow (Gemini)…"))
				t, err := auth.LoginGemini(ctx)
				if err != nil {
					return fmt.Errorf("login: %w", err)
				}
				path := auth.DefaultGeminiTokenPath()
				gt := &auth.GeminiToken{
					Access:    t.Access,
					Refresh:   t.Refresh,
					ExpiresMS: t.ExpiresMS,
				}
				if err := auth.SaveGemini(path, gt); err != nil {
					return fmt.Errorf("login: save token: %w", err)
				}
				fmt.Println(ui.OK("Logged in to Gemini successfully"))
				fmt.Println(ui.Dim("Token saved to: ") + path)

			case "anthropic", "claude":
				fmt.Println(ui.Info("Anthropic uses API keys (no OAuth flow available)."))
				fmt.Println(ui.Info("Get your key from: https://console.anthropic.com/settings/keys"))
				fmt.Print("Paste your API key: ")
				scanner := bufio.NewScanner(os.Stdin)
				if !scanner.Scan() {
					return fmt.Errorf("login: no input")
				}
				apiKey := strings.TrimSpace(scanner.Text())
				if apiKey == "" {
					return fmt.Errorf("login: empty API key")
				}
				if !strings.HasPrefix(apiKey, "sk-ant-") {
					fmt.Println(ui.Warn("Key doesn't start with sk-ant- — are you sure this is correct?"))
				}
				path := auth.DefaultClaudeTokenPath()
				ct := &auth.ClaudeToken{APIKey: apiKey}
				if err := auth.SaveClaude(path, ct); err != nil {
					return fmt.Errorf("login: save key: %w", err)
				}
				fmt.Println(ui.OK("API key saved"))
				fmt.Println(ui.Dim("Stored at: ") + path)

			case "minimax":
				fmt.Println(ui.Info("Starting OAuth Device Flow (MiniMax Coding Plan)…"))
				t, err := auth.LoginMiniMax(ctx)
				if err != nil {
					return fmt.Errorf("login: %w", err)
				}
				path := auth.DefaultMiniMaxTokenPath()
				if err := auth.SaveMiniMax(path, t); err != nil {
					return fmt.Errorf("login: save token: %w", err)
				}
				fmt.Println(ui.OK("Logged in to MiniMax successfully"))
				fmt.Println(ui.Dim("Token saved to: ") + path)

			default:
				return fmt.Errorf("login: unknown provider %q (supported: codex, gemini, anthropic, claude, minimax)", provider)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "OAuth provider (codex, gemini, anthropic, claude, minimax)")
	return cmd
}

func logoutCmd() *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored OAuth token",
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			switch provider {
			case "codex", "":
				path = auth.DefaultTokenPath()
			case "gemini":
				path = auth.DefaultGeminiTokenPath()
			case "anthropic", "claude":
				path = auth.DefaultClaudeTokenPath()
			case "minimax":
				path = auth.DefaultMiniMaxTokenPath()
			default:
				return fmt.Errorf("logout: unknown provider %q (supported: codex, gemini, anthropic, claude, minimax)", provider)
			}
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
	cmd.Flags().StringVar(&provider, "provider", "codex", "provider (codex, gemini, anthropic, claude, minimax)")
	return cmd
}

func doctorCmd(cfgPath *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check config, provider auth, tool availability, and run dir",
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
			validation := config.ValidateConfigPath(cfgFile)
			printConfigValidation(validation)
			if validation.HasErrors() {
				ok = false
			}
			if dryRun {
				fmt.Println(ui.Info("Dry run: skipped provider authentication, network probes, tool binary probes, and runs/ write check."))
				fmt.Println()
				if ok {
					fmt.Println(ui.OK(ui.Bold("All validation checks passed")))
				} else {
					fmt.Println(ui.Fail(ui.Bold("Validation checks failed — fix issues above before running")))
				}
				return nil
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				fmt.Println(ui.Fail("Config parse error: " + err.Error()))
				return nil
			}
			reg := buildToolRegistry(cfg)
			if err := reg.Validate(); err != nil {
				fmt.Println(ui.Fail("Tool registry invalid: " + err.Error()))
				ok = false
			} else {
				fmt.Println(ui.OK("Effective tools: " + enabledToolSummary(reg)))
			}
			if sandboxBackendNeedsDocker(cfg) {
				if p, err := findInPath("docker"); err == nil && p != "" {
					fmt.Println(ui.OK("docker: " + p))
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					version, err := dockerServerVersion(ctx)
					cancel()
					if err != nil {
						fmt.Println(ui.Fail("Docker daemon unavailable: " + err.Error()))
						ok = false
					} else {
						fmt.Println(ui.OK("Sandbox backend: docker (" + version + ")"))
					}
				} else {
					fmt.Println(ui.Fail("docker not found — sandbox backend docker will fail"))
					ok = false
				}
				if strings.TrimSpace(cfg.Sandbox.Image) == "" {
					fmt.Println(ui.Fail("Sandbox image not configured for docker backend"))
					ok = false
				} else {
					fmt.Println(ui.OK("Sandbox image: " + cfg.Sandbox.Image))
				}
			} else {
				fmt.Println(ui.OK("Sandbox backend: host"))
			}
			line, unhealthy, unavailable := executorResourceStatusLine()
			if unhealthy {
				fmt.Println(ui.Fail(line))
				ok = false
			} else if unavailable {
				fmt.Println(ui.Warn(line))
			} else {
				fmt.Println(ui.OK(line))
			}

			printOAuthConfigStatus := func(name string, err error) {
				credsPath := auth.DefaultCredentialsPath()
				if err == nil {
					fmt.Println(ui.OK(fmt.Sprintf("Provider %s: OAuth client secrets resolved", name)))
					return
				}
				fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: OAuth client secrets missing", name)))
				fmt.Println(ui.Dim("  " + strings.ReplaceAll(err.Error(), "\n", "\n  ")))
				fmt.Println(ui.Dim("  Plaintext fallback path: " + credsPath))
				ok = false
			}

			// 2. Provider auth
			// Only fail on the default provider; others just warn
			defaultProvider := cfg.Defaults.Provider
			providerIssue := func(name, msg string) {
				if name == defaultProvider {
					fmt.Println(ui.Fail(msg))
					ok = false
				} else {
					fmt.Println(ui.Warn(msg))
				}
			}
			for name, pc := range cfg.Providers {
				switch pc.Type {
				case "codex":
					_, credsErr := auth.LoadCodexCredentials()
					printOAuthConfigStatus(name, credsErr)
					tokenPath := auth.DefaultTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: no token at %s — run 'v100 login'", name, tokenPath))
					}
				case "gemini":
					_, credsErr := auth.LoadGeminiCredentials()
					printOAuthConfigStatus(name, credsErr)
					tokenPath := auth.DefaultGeminiTokenPath()
					hasToken := false
					if _, err := os.Stat(tokenPath); err == nil {
						hasToken = true
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: no token at %s — run 'v100 login --provider gemini'", name, tokenPath))
					}
					if hasToken {
						pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
						t0 := time.Now()
						pingReq, _ := http.NewRequestWithContext(pingCtx, http.MethodGet, "https://cloudcode-pa.googleapis.com/", nil)
						pingResp, pingErr := http.DefaultClient.Do(pingReq)
						pingCancel()
						if pingErr != nil {
							providerIssue(name, fmt.Sprintf("Provider %s: connectivity FAIL (%v)", name, pingErr))
						} else {
							_ = pingResp.Body.Close()
							latency := time.Since(t0).Milliseconds()
							fmt.Println(ui.OK(fmt.Sprintf("Provider %s: reachable (%dms)", name, latency)))
						}
					}
				case "ollama":
					baseURL := strings.TrimSpace(pc.BaseURL)
					if baseURL == "" {
						baseURL = "http://localhost:11434"
					}
					// Warn when env vars differ from config
					envURL := os.Getenv("OLLAMA_BASE_URL")
					if envURL == "" {
						envURL = os.Getenv("OLLAMA_HOST")
					}
					if envURL != "" && envURL != baseURL {
						fmt.Println(ui.Warn(fmt.Sprintf("Provider %s: env OLLAMA_BASE_URL=%s differs from config base_url=%s (config wins)", name, envURL, baseURL)))
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
					resp, err := http.DefaultClient.Do(req)
					cancel()
					if err != nil {
						providerIssue(name, fmt.Sprintf("Provider %s: cannot reach %s (%v)", name, baseURL, err))
						break
					}
					_ = resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: reachable at %s", name, baseURL)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: %s returned HTTP %d", name, baseURL, resp.StatusCode))
					}
				case "llamacpp", "llama.cpp", "llama-cpp":
					baseURL := strings.TrimSpace(pc.BaseURL)
					if baseURL == "" {
						baseURL = "http://127.0.0.1:19091/v1"
					}
					envURL := os.Getenv("LLAMA_CPP_BASE_URL")
					if envURL == "" {
						envURL = os.Getenv("LLAMA_SERVER_URL")
					}
					if envURL == "" {
						envURL = os.Getenv("LLAMA_BASE_URL")
					}
					if envURL != "" && envURL != baseURL {
						fmt.Println(ui.Warn(fmt.Sprintf("Provider %s: env LLAMA_CPP_BASE_URL=%s differs from config base_url=%s (config wins)", name, envURL, baseURL)))
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
					resp, err := http.DefaultClient.Do(req)
					cancel()
					if err != nil {
						providerIssue(name, fmt.Sprintf("Provider %s: cannot reach %s (%v)", name, baseURL, err))
						break
					}
					_ = resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: reachable at %s", name, baseURL)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: %s returned HTTP %d", name, baseURL, resp.StatusCode))
					}
				case "anthropic":
					authEnv := pc.Auth.Env
					if authEnv == "" {
						authEnv = "ANTHROPIC_API_KEY"
					}
					var anthropicKey string
					tokenPath := auth.DefaultClaudeTokenPath()
					if ct, err := auth.LoadClaudeWithEnv(tokenPath, authEnv); err == nil && ct.Valid() {
						hint := ct.APIKey
						anthropicKey = ct.APIKey
						if len(hint) > 12 {
							hint = hint[:8] + "..." + hint[len(hint)-4:]
						}
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: API key resolved (%s)", name, hint)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: no key — set %s, store provider_anthropic_api_key in a secret manager, or run 'v100 login --provider anthropic'", name, authEnv))
					}
					if anthropicKey != "" {
						pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
						t0 := time.Now()
						pingReq, _ := http.NewRequestWithContext(pingCtx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
						pingReq.Header.Set("x-api-key", anthropicKey)
						pingReq.Header.Set("anthropic-version", "2023-06-01")
						pingResp, pingErr := http.DefaultClient.Do(pingReq)
						pingCancel()
						if pingErr != nil {
							providerIssue(name, fmt.Sprintf("Provider %s: connectivity FAIL (%v)", name, pingErr))
						} else {
							_ = pingResp.Body.Close()
							latency := time.Since(t0).Milliseconds()
							if pingResp.StatusCode == 200 || pingResp.StatusCode == 401 {
								fmt.Println(ui.OK(fmt.Sprintf("Provider %s: reachable (%dms)", name, latency)))
							} else {
								providerIssue(name, fmt.Sprintf("Provider %s: connectivity FAIL HTTP %d (%dms)", name, pingResp.StatusCode, latency))
							}
						}
					}
				case "minimax":
					_, credsErr := auth.LoadMiniMaxCredentials()
					printOAuthConfigStatus(name, credsErr)
					tokenPath := auth.DefaultMiniMaxTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						providerIssue(name, fmt.Sprintf("Provider %s: no token at %s — run 'v100 login --provider minimax'", name, tokenPath))
					}
				default:
					key := os.Getenv(pc.Auth.Env)
					if key == "" {
						providerIssue(name, fmt.Sprintf("Provider %s: env var %s not set", name, pc.Auth.Env))
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
					_ = f.Close()
					_ = os.Remove(testFile)
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
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate config and behavior dirs without provider/network/write checks")
	return cmd
}

func printConfigValidation(result *config.ValidationResult) {
	if result == nil {
		return
	}
	for _, finding := range result.Findings {
		msg := finding.Message
		if strings.TrimSpace(finding.Path) != "" {
			msg = finding.Path + ": " + msg
		}
		switch finding.Severity {
		case config.ValidationError:
			fmt.Println(ui.Fail(msg))
		case config.ValidationWarning:
			fmt.Println(ui.Warn(msg))
		case config.ValidationInfo:
			fmt.Println(ui.Dim(msg))
		}
	}
	errors, warnings, _ := result.Counts()
	if errors > 0 {
		fmt.Println(ui.Fail(fmt.Sprintf("Config validation: %d error(s), %d warning(s)", errors, warnings)))
		return
	}
	if warnings > 0 {
		fmt.Println(ui.Warn(fmt.Sprintf("Config validation: 0 error(s), %d warning(s)", warnings)))
		return
	}
	fmt.Println(ui.OK("Config validation: ok"))
}

func executorResourceStatusLine() (string, bool, bool) {
	stats, err := executor.CurrentResourceStats()
	if err != nil {
		return "Executor resources: unavailable (" + err.Error() + ")", false, true
	}
	line := fmt.Sprintf(
		"Executor resources: open_fds=%d subprocesses=%d zombies=%d process_pool=%d/%d",
		stats.OpenFDs,
		stats.RunningSubprocesses,
		stats.ZombieSubprocesses,
		stats.ProcessPoolUsed,
		stats.ProcessPoolLimit,
	)
	if stats.FDSoftLimit > 0 {
		line += fmt.Sprintf(" fd_soft_limit=%d", stats.FDSoftLimit)
	}
	if warning := stats.ExhaustionWarning(); warning != "" {
		return line + " (" + warning + ")", true, false
	}
	return line, false, false
}

func exportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <run_id>",
		Short: "Export a run (trace, meta, and sandbox state) to a tar.gz archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]
			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			exportDir := "exports"
			if err := os.MkdirAll(exportDir, 0o755); err != nil {
				return err
			}

			exportPath := filepath.Join(exportDir, fmt.Sprintf("v100-run-%s.tar.gz", runID))
			f, err := os.Create(exportPath)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()

			gw := gzip.NewWriter(f)
			defer func() { _ = gw.Close() }()
			tw := tar.NewWriter(gw)
			defer func() { _ = tw.Close() }()

			// 1. Add trace.jsonl
			tracePath := filepath.Join(runDir, "trace.jsonl")
			if err := addFileToTar(tw, tracePath, "trace.jsonl"); err != nil {
				return fmt.Errorf("add trace: %w", err)
			}

			// 2. Add meta.json
			metaPath := filepath.Join(runDir, "meta.json")
			if err := addFileToTar(tw, metaPath, "meta.json"); err != nil {
				return fmt.Errorf("add meta: %w", err)
			}

			// 3. Add workspace snapshot if it exists
			snapPath := filepath.Join(runDir, "sandbox.snapshot")
			if _, err := os.Stat(snapPath); err == nil {
				if err := addFileToTar(tw, snapPath, "sandbox.snapshot"); err != nil {
					return fmt.Errorf("add snapshot: %w", err)
				}
			}
			snapshotDir := filepath.Join(runDir, "snapshots")
			if _, err := os.Stat(snapshotDir); err == nil {
				if err := addPathToTar(tw, snapshotDir, "snapshots"); err != nil {
					return fmt.Errorf("add snapshots: %w", err)
				}
			}

			fmt.Printf("Run %s exported to: %s\n", ui.Info(runID), ui.OK(exportPath))
			return nil
		},
	}
}

func addFileToTar(tw *tar.Writer, srcPath, tarPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = tarPath

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(tw, f)
	return err
}

func addPathToTar(tw *tar.Writer, srcPath, tarPath string) error {
	return filepath.WalkDir(srcPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcPath, path)
		if err != nil {
			return err
		}
		name := tarPath
		if rel != "." {
			name = filepath.ToSlash(filepath.Join(tarPath, rel))
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = name
		if d.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
}

func sandboxBackendNeedsDocker(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.Sandbox.Backend), "docker")
}

func dockerServerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("empty docker server version response")
	}
	return version, nil
}

package main

import (
	"bufio"
	"context"
	"fmt"
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
	"github.com/tripledoublev/v100/internal/policy"
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
	return &cobra.Command{
		Use:   "providers",
		Short: "List configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			for name, pc := range cfg.Providers {
				pc = normalizedProviderConfig(pc)
				fmt.Printf("%-15s type=%-10s model=%s\n", name, pc.Type, pc.DefaultModel)
			}
			return nil
		},
	}
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

			credsPath := auth.DefaultCredentialsPath()
			switch _, err := os.Stat(credsPath); {
			case err == nil:
				fmt.Println(ui.OK("OAuth client config found at " + credsPath))
			case os.IsNotExist(err):
				if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(credsPath, []byte(auth.CredentialsTemplate()), 0o600); err != nil {
					return err
				}
				fmt.Println(ui.OK("OAuth client template written to " + credsPath))
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

			case "anthropic":
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

			default:
				return fmt.Errorf("login: unknown provider %q (supported: codex, gemini, anthropic)", provider)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "codex", "OAuth provider (codex, gemini, anthropic)")
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
			case "anthropic":
				path = auth.DefaultClaudeTokenPath()
			default:
				return fmt.Errorf("logout: unknown provider %q (supported: codex, gemini, anthropic)", provider)
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
	cmd.Flags().StringVar(&provider, "provider", "codex", "provider (codex, gemini, anthropic)")
	return cmd
}

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

			printOAuthConfigStatus := func(name string, err error) {
				credsPath := auth.DefaultCredentialsPath()
				if err == nil {
					fmt.Println(ui.OK(fmt.Sprintf("Provider %s: OAuth client config at %s", name, credsPath)))
					return
				}
				fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: OAuth client config invalid at %s", name, credsPath)))
				fmt.Println(ui.Dim("  " + strings.ReplaceAll(err.Error(), "\n", "\n  ")))
				ok = false
			}

			// 2. Provider auth
			for name, pc := range cfg.Providers {
				switch pc.Type {
				case "codex":
					_, credsErr := auth.LoadCodexCredentials()
					printOAuthConfigStatus(name, credsErr)
					tokenPath := auth.DefaultTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: no token at %s — run 'v100 login'", name, tokenPath)))
						ok = false
					}
				case "gemini":
					_, credsErr := auth.LoadGeminiCredentials()
					printOAuthConfigStatus(name, credsErr)
					tokenPath := auth.DefaultGeminiTokenPath()
					if _, err := os.Stat(tokenPath); err == nil {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: token at %s", name, tokenPath)))
					} else {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: no token at %s — run 'v100 login --provider gemini'", name, tokenPath)))
						ok = false
					}
				case "ollama":
					baseURL := strings.TrimSpace(pc.BaseURL)
					if baseURL == "" {
						baseURL = "http://localhost:11434"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
					resp, err := http.DefaultClient.Do(req)
					cancel()
					if err != nil {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: cannot reach %s (%v)", name, baseURL, err)))
						ok = false
						break
					}
					_ = resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: reachable at %s", name, baseURL)))
					} else {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: %s returned HTTP %d", name, baseURL, resp.StatusCode)))
						ok = false
					}
				case "anthropic":
					authEnv := pc.Auth.Env
					if authEnv == "" {
						authEnv = "ANTHROPIC_API_KEY"
					}
					// Check stored key first, then env var
					tokenPath := auth.DefaultClaudeTokenPath()
					if ct, err := auth.LoadClaude(tokenPath); err == nil && ct.Valid() {
						hint := ct.APIKey
						if len(hint) > 12 {
							hint = hint[:8] + "..." + hint[len(hint)-4:]
						}
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: stored key at %s (%s)", name, tokenPath, hint)))
					} else if key := os.Getenv(authEnv); key != "" {
						fmt.Println(ui.OK(fmt.Sprintf("Provider %s: %s set (%d chars)", name, authEnv, len(key))))
					} else {
						fmt.Println(ui.Fail(fmt.Sprintf("Provider %s: no key — run 'v100 login --provider anthropic' or set %s", name, authEnv)))
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

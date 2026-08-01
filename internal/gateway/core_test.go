package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
)

func TestParseCommandNormalizesNameAndArg(t *testing.T) {
	cmd, ok := ParseCommand("  /Model@V100Bot   glm-4.6  ")
	if !ok {
		t.Fatal("expected command")
	}
	if cmd.Name != "model" || cmd.Arg != "glm-4.6" {
		t.Fatalf("command = %#v", cmd)
	}
	if _, ok := ParseCommand("hello /model glm"); ok {
		t.Fatal("non-command message parsed as command")
	}
}

func TestCommandAllowedUsesProfileAllowlist(t *testing.T) {
	profile := config.GatewayProfile{AllowedCommands: []string{"help", "/reset"}}
	if !CommandAllowed(profile, true, "/help") {
		t.Fatal("expected help to be allowed")
	}
	if !CommandAllowed(profile, true, "reset") {
		t.Fatal("expected reset to be allowed")
	}
	if CommandAllowed(profile, true, "profile") {
		t.Fatal("expected profile to be denied")
	}
	if !CommandAllowed(config.GatewayProfile{}, false, "profile") {
		t.Fatal("missing profile should preserve legacy allow behavior")
	}
	if CommandAllowed(config.GatewayProfile{}, true, "help") {
		t.Fatal("empty profile allowlist should deny commands")
	}
}

func TestResolveProfilePrefersChatProfile(t *testing.T) {
	profiles := map[string]config.GatewayProfile{
		"default": {Provider: "glm"},
		"chat":    {Provider: "ollama"},
	}
	runtime := ResolveProfile(profiles, "default", map[string]string{"42": "chat"}, "42")
	if !runtime.OK || runtime.Name != "chat" || runtime.Profile.Provider != "ollama" {
		t.Fatalf("chat runtime = %#v", runtime)
	}
	runtime = ResolveProfile(profiles, "default", nil, "99")
	if !runtime.OK || runtime.Name != "default" || runtime.Profile.Provider != "glm" {
		t.Fatalf("default runtime = %#v", runtime)
	}
	runtime = ResolveProfile(profiles, "missing", nil, "99")
	if runtime.OK || runtime.Name != "missing" {
		t.Fatalf("missing runtime = %#v", runtime)
	}
}

func TestApplyProfileToSessionNewCopiesSandboxAndPrompt(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	params := acp.SessionNewParams{}
	err := ApplyProfileToSessionNew(&params, ProfileRuntime{
		Name: "news_fr",
		OK:   true,
		Profile: config.GatewayProfile{
			Tools:            []string{"news_fetch", "translate"},
			Dangerous:        []string{},
			Provider:         "glm",
			Model:            "glm-4.6",
			Solver:           "react",
			SystemPrompt:     "inline",
			SystemPromptPath: promptPath,
			NetworkTier:      "research",
			BudgetSteps:      12,
			BudgetTokens:     40000,
			BudgetCostUSD:    0.25,
		},
	}, dir)
	if err != nil {
		t.Fatalf("ApplyProfileToSessionNew returned error: %v", err)
	}
	if strings.Join(params.Tools, ",") != "news_fetch,translate" {
		t.Fatalf("tools = %v", params.Tools)
	}
	if params.Dangerous == nil || len(params.Dangerous) != 0 {
		t.Fatalf("dangerous = %#v, want explicit empty", params.Dangerous)
	}
	if params.Provider != "glm" || params.Model != "glm-4.6" || params.Solver != "react" {
		t.Fatalf("runtime = %#v", params)
	}
	if params.SystemPrompt != "from file" || params.NetworkTier != "research" {
		t.Fatalf("prompt/network = %#v", params)
	}
	if params.BudgetSteps != 12 || params.BudgetTokens != 40000 || params.BudgetCostUSD != 0.25 {
		t.Fatalf("budgets = %#v", params)
	}
}

func TestApplyProfileToSessionNewPreservesExplicitZeroBudget(t *testing.T) {
	params := acp.SessionNewParams{}
	err := ApplyProfileToSessionNew(&params, ProfileRuntime{
		Name: "unlimited",
		OK:   true,
		Profile: config.GatewayProfile{
			Tools:           []string{"web_search"},
			BudgetSteps:     0,
			BudgetStepsSet:  true,
			BudgetTokens:    0,
			BudgetTokensSet: true,
			BudgetCostUSD:   0,
			BudgetCostSet:   true,
		},
	}, "")
	if err != nil {
		t.Fatalf("ApplyProfileToSessionNew returned error: %v", err)
	}
	if params.BudgetSteps != 0 || !params.BudgetStepsSet {
		t.Fatalf("budget steps = %d set=%v, want explicit zero", params.BudgetSteps, params.BudgetStepsSet)
	}
	if params.BudgetTokens != 0 || !params.BudgetTokensSet {
		t.Fatalf("budget tokens = %d set=%v, want explicit zero", params.BudgetTokens, params.BudgetTokensSet)
	}
	if params.BudgetCostUSD != 0 || !params.BudgetCostSet {
		t.Fatalf("budget cost = %f set=%v, want explicit zero", params.BudgetCostUSD, params.BudgetCostSet)
	}
}

func TestApplyProfileToSessionResumeMatchesNewRestrictions(t *testing.T) {
	params := acp.SessionResumeParams{RunID: "run-1"}
	err := ApplyProfileToSessionResume(&params, ProfileRuntime{
		Name: "restored",
		OK:   true,
		Profile: config.GatewayProfile{
			Provider:        "glm",
			Model:           "glm-4.6",
			Solver:          "react",
			Tools:           []string{"news_fetch"},
			Dangerous:       []string{},
			SystemPrompt:    "restored prompt",
			NetworkTier:     "research",
			BudgetSteps:     0,
			BudgetStepsSet:  true,
			BudgetTokens:    1000000,
			BudgetTokensSet: true,
			BudgetCostUSD:   0,
			BudgetCostSet:   true,
		},
	}, "")
	if err != nil {
		t.Fatalf("ApplyProfileToSessionResume returned error: %v", err)
	}
	if params.RunID != "run-1" {
		t.Fatalf("run ID changed: %#v", params)
	}
	if params.Provider != "glm" || params.Model != "glm-4.6" || params.Solver != "react" {
		t.Fatalf("runtime = %#v", params)
	}
	if strings.Join(params.Tools, ",") != "news_fetch" || params.Dangerous == nil || len(params.Dangerous) != 0 {
		t.Fatalf("tool restrictions = %#v", params)
	}
	if params.SystemPrompt != "restored prompt" || params.NetworkTier != "research" {
		t.Fatalf("prompt/network = %#v", params)
	}
	if params.BudgetSteps == nil || *params.BudgetSteps != 0 || params.BudgetTokens == nil || *params.BudgetTokens != 1000000 || params.BudgetCostUSD == nil || *params.BudgetCostUSD != 0 {
		t.Fatalf("budgets = %#v", params)
	}
}

func TestApplyProfileToSessionNewRejectsNegativeBudgets(t *testing.T) {
	tests := []struct {
		name    string
		profile config.GatewayProfile
	}{
		{
			name: "steps",
			profile: config.GatewayProfile{
				BudgetSteps:    -1,
				BudgetStepsSet: true,
			},
		},
		{
			name: "tokens",
			profile: config.GatewayProfile{
				BudgetTokens:    -1,
				BudgetTokensSet: true,
			},
		},
		{
			name: "cost",
			profile: config.GatewayProfile{
				BudgetCostUSD: -0.01,
				BudgetCostSet: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ApplyProfileToSessionNew(&acp.SessionNewParams{}, ProfileRuntime{
				Name:    "invalid",
				OK:      true,
				Profile: tt.profile,
			}, "")
			if err == nil {
				t.Fatal("ApplyProfileToSessionNew returned nil, want budget validation error")
			}
		})
	}
}

func TestReconfigureParamsMapsRuntimeCommands(t *testing.T) {
	params, ok := ReconfigureParams("session-1", Command{Name: "provider", Arg: "ollama"})
	if !ok || params.SessionID != "session-1" || params.Provider != "ollama" {
		t.Fatalf("provider params = %#v ok=%v", params, ok)
	}
	params, ok = ReconfigureParams("session-1", Command{Name: "model", Arg: "llama3.1"})
	if !ok || params.Model != "llama3.1" {
		t.Fatalf("model params = %#v ok=%v", params, ok)
	}
	params, ok = ReconfigureParams("session-1", Command{Name: "solver", Arg: "react"})
	if !ok || params.Solver != "react" {
		t.Fatalf("solver params = %#v ok=%v", params, ok)
	}
	if _, ok := ReconfigureParams("session-1", Command{Name: "model"}); ok {
		t.Fatal("expected missing value to fail")
	}
}

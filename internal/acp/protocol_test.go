package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestErrorMessageCoversACPErrorCodes(t *testing.T) {
	cases := map[int]string{
		ErrParse:                      "parse error",
		ErrInvalidRequest:             "invalid request",
		ErrMethodNotFound:             "method not found",
		ErrInvalidParams:              "invalid params",
		ErrInternal:                   "internal error",
		ErrSessionNotFound:            "session not found",
		ErrSessionAlreadyExists:       "session already exists",
		ErrSessionBusy:                "session busy",
		ErrSessionClosing:             "session closing",
		ErrUnsupportedProtocolVersion: "unsupported protocol version",
		ErrInvalidSessionConfig:       "invalid session config",
		ErrProviderConfiguration:      "provider configuration error",
		1234:                          "unknown error",
	}
	for code, want := range cases {
		if got := ErrorMessage(code); got != want {
			t.Fatalf("ErrorMessage(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestSessionNewParamsJSONPreservesExplicitZeroBudgets(t *testing.T) {
	payload, err := json.Marshal(SessionNewParams{
		SessionID:       "zero-budgets",
		BudgetSteps:     0,
		BudgetStepsSet:  true,
		BudgetTokens:    0,
		BudgetTokensSet: true,
		BudgetCostUSD:   0,
		BudgetCostSet:   true,
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{
		`"budget_steps":0`,
		`"budget_tokens":0`,
		`"budget_cost_usd":0`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %s: %s", want, text)
		}
	}

	var params SessionNewParams
	if err := json.Unmarshal(payload, &params); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if params.BudgetSteps != 0 || !params.BudgetStepsSet {
		t.Fatalf("steps = %d set=%v, want explicit zero", params.BudgetSteps, params.BudgetStepsSet)
	}
	if params.BudgetTokens != 0 || !params.BudgetTokensSet {
		t.Fatalf("tokens = %d set=%v, want explicit zero", params.BudgetTokens, params.BudgetTokensSet)
	}
	if params.BudgetCostUSD != 0 || !params.BudgetCostSet {
		t.Fatalf("cost = %f set=%v, want explicit zero", params.BudgetCostUSD, params.BudgetCostSet)
	}
}

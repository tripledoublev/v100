package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestHandleInteractiveBudgetExceededRetriesNearExhaustion(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxTokens: 100, UsedTokens: 95})
	decision := handleInteractiveBudgetExceeded(budget, &core.ErrBudgetExceeded{Reason: "tokens nearly exhausted: 95/100"}, func(string) bool {
		return true
	})

	if decision != interactiveBudgetRetry {
		t.Fatalf("decision = %v, want retry", decision)
	}
	if budget.Budget().MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 after override", budget.Budget().MaxTokens)
	}
}

func TestHandleInteractiveBudgetExceededRetriesStepExhaustion(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxSteps: 3, UsedSteps: 3})
	decision := handleInteractiveBudgetExceeded(budget, &core.ErrBudgetExceeded{Reason: "steps exhausted: 3/3"}, func(string) bool {
		return true
	})

	if decision != interactiveBudgetRetry {
		t.Fatalf("decision = %v, want retry", decision)
	}
	if budget.Budget().MaxSteps != 0 {
		t.Fatalf("MaxSteps = %d, want 0 after override", budget.Budget().MaxSteps)
	}
}

func TestHandleInteractiveBudgetExceededContinuesAfterTokenBudgetHit(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxTokens: 100, UsedTokens: 100})
	decision := handleInteractiveBudgetExceeded(budget, &core.ErrBudgetExceeded{Reason: "tokens: used 100 of 100"}, func(string) bool {
		return true
	})

	if decision != interactiveBudgetContinue {
		t.Fatalf("decision = %v, want continue", decision)
	}
	if budget.Budget().MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 after override", budget.Budget().MaxTokens)
	}
}

func TestHandleInteractiveBudgetExceededDeclineLeavesBudgetStrict(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxTokens: 100, UsedTokens: 100})
	decision := handleInteractiveBudgetExceeded(budget, &core.ErrBudgetExceeded{Reason: "tokens: used 100 of 100"}, func(string) bool {
		return false
	})

	if decision != interactiveBudgetUnhandled {
		t.Fatalf("decision = %v, want unhandled", decision)
	}
	if budget.Budget().MaxTokens != 100 {
		t.Fatalf("MaxTokens = %d, want 100", budget.Budget().MaxTokens)
	}
}

func TestHandleInteractiveBudgetExceededContinuesAfterCostBudgetHit(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxCostUSD: 1.0, UsedCostUSD: 1.0})
	decision := handleInteractiveBudgetExceeded(budget, &core.ErrBudgetExceeded{Reason: "cost: used $1.0000 of $1.0000"}, func(string) bool {
		return true
	})

	if decision != interactiveBudgetContinue {
		t.Fatalf("decision = %v, want continue", decision)
	}
	if budget.Budget().MaxCostUSD != 0 {
		t.Fatalf("MaxCostUSD = %f, want 0 after override", budget.Budget().MaxCostUSD)
	}
}

func TestPromptContinueWithoutBudgetLimitAcceptsContinueAnswers(t *testing.T) {
	var out bytes.Buffer
	ok := promptContinueWithoutBudgetLimit(strings.NewReader("continue\n"), &out, "tokens: used 100 of 100")
	if !ok {
		t.Fatal("expected continue prompt to accept \"continue\"")
	}
	if !strings.Contains(out.String(), "token budget hit: tokens: used 100 of 100") {
		t.Fatalf("prompt output missing reason in %q", out.String())
	}
}

func TestRunInteractiveStepRetriesPreflightBudgetErrors(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxSteps: 1, UsedSteps: 1})
	calls := 0

	err := runInteractiveStep(func() error {
		calls++
		if calls == 1 {
			return &core.ErrBudgetExceeded{Reason: "steps exhausted: 1/1"}
		}
		return nil
	}, budget, func(string) bool {
		return true
	})

	if err != nil {
		t.Fatalf("runInteractiveStep returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if budget.Budget().MaxSteps != 0 {
		t.Fatalf("MaxSteps = %d, want 0 after override", budget.Budget().MaxSteps)
	}
}

func TestRunInteractiveStepTreatsPostStepBudgetErrorsAsHandled(t *testing.T) {
	budget := core.NewBudgetTracker(&core.Budget{MaxCostUSD: 1.0, UsedCostUSD: 1.0})
	calls := 0

	err := runInteractiveStep(func() error {
		calls++
		return &core.ErrBudgetExceeded{Reason: "cost: used $1.0000 of $1.0000"}
	}, budget, func(string) bool {
		return true
	})

	if err != nil {
		t.Fatalf("runInteractiveStep returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if budget.Budget().MaxCostUSD != 0 {
		t.Fatalf("MaxCostUSD = %f, want 0 after override", budget.Budget().MaxCostUSD)
	}
}

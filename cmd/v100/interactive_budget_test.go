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

func TestHandleInteractiveBudgetExceededContinuesAfterStepBudgetHit(t *testing.T) {
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

func TestPromptContinueWithoutTokenLimitAcceptsContinueAnswers(t *testing.T) {
	var out bytes.Buffer
	ok := promptContinueWithoutTokenLimit(strings.NewReader("continue\n"), &out, "tokens: used 100 of 100")
	if !ok {
		t.Fatal("expected continue prompt to accept \"continue\"")
	}
	if !strings.Contains(out.String(), "token budget hit: tokens: used 100 of 100") {
		t.Fatalf("prompt output missing reason in %q", out.String())
	}
}

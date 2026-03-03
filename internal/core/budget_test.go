package core_test

import (
	"errors"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestBudgetSteps(t *testing.T) {
	b := &core.Budget{MaxSteps: 3}
	tracker := core.NewBudgetTracker(b)

	if err := tracker.AddStep(); err != nil {
		t.Errorf("unexpected error on step 1: %v", err)
	}
	if err := tracker.AddStep(); err != nil {
		t.Errorf("unexpected error on step 2: %v", err)
	}
	err := tracker.AddStep()
	if err == nil {
		t.Error("expected budget exceeded error on step 3")
	}
	var budgetErr *core.ErrBudgetExceeded
	if !errors.As(err, &budgetErr) {
		t.Errorf("expected ErrBudgetExceeded, got %T", err)
	}
}

func TestBudgetTokens(t *testing.T) {
	b := &core.Budget{MaxTokens: 100}
	tracker := core.NewBudgetTracker(b)

	if err := tracker.AddTokens(40, 40); err != nil {
		t.Error("unexpected error: should be within budget")
	}
	err := tracker.AddTokens(10, 15) // total = 105 >= 100
	if err == nil {
		t.Error("expected budget exceeded error")
	}
}

func TestBudgetNoLimits(t *testing.T) {
	b := &core.Budget{} // all zeroes = unlimited
	tracker := core.NewBudgetTracker(b)

	for i := 0; i < 1000; i++ {
		if err := tracker.AddStep(); err != nil {
			t.Fatalf("unexpected error at step %d: %v", i, err)
		}
	}
	if err := tracker.AddTokens(1_000_000, 1_000_000); err != nil {
		t.Error("unexpected error for unlimited tokens")
	}
}

func TestBudgetCost(t *testing.T) {
	b := &core.Budget{MaxCostUSD: 1.00}
	tracker := core.NewBudgetTracker(b)

	if err := tracker.AddCost(0.50); err != nil {
		t.Error("unexpected error")
	}
	err := tracker.AddCost(0.60) // total = 1.10 >= 1.00
	if err == nil {
		t.Error("expected cost budget error")
	}
}

func TestBudgetSummary(t *testing.T) {
	b := &core.Budget{MaxSteps: 10, MaxTokens: 500}
	tracker := core.NewBudgetTracker(b)
	_ = tracker.AddStep()
	_ = tracker.AddTokens(100, 50)

	s := tracker.Summary()
	if s == "" {
		t.Error("expected non-empty summary")
	}
}

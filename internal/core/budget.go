package core

import "fmt"

// BudgetTracker wraps a Budget and provides enforcement methods.
type BudgetTracker struct {
	b *Budget
}

// NewBudgetTracker creates a tracker for the given budget.
func NewBudgetTracker(b *Budget) *BudgetTracker {
	return &BudgetTracker{b: b}
}

// AddStep increments UsedSteps and checks the limit.
func (t *BudgetTracker) AddStep() error {
	t.b.UsedSteps++
	if t.b.MaxSteps > 0 && t.b.UsedSteps >= t.b.MaxSteps {
		return &ErrBudgetExceeded{Reason: fmt.Sprintf("steps: used %d of %d", t.b.UsedSteps, t.b.MaxSteps)}
	}
	return nil
}

// AddTokens adds token consumption and checks the limit.
func (t *BudgetTracker) AddTokens(input, output int) error {
	t.b.UsedTokens += input + output
	if t.b.MaxTokens > 0 && t.b.UsedTokens >= t.b.MaxTokens {
		return &ErrBudgetExceeded{Reason: fmt.Sprintf("tokens: used %d of %d", t.b.UsedTokens, t.b.MaxTokens)}
	}
	return nil
}

// AddCost adds cost and checks the limit.
func (t *BudgetTracker) AddCost(costUSD float64) error {
	t.b.UsedCostUSD += costUSD
	if t.b.MaxCostUSD > 0 && t.b.UsedCostUSD >= t.b.MaxCostUSD {
		return &ErrBudgetExceeded{Reason: fmt.Sprintf("cost: used $%.4f of $%.4f", t.b.UsedCostUSD, t.b.MaxCostUSD)}
	}
	return nil
}

// Budget returns the current budget state.
func (t *BudgetTracker) Budget() Budget {
	return *t.b
}

// RemainingSteps returns how many steps remain (0 means unlimited).
func (t *BudgetTracker) RemainingSteps() int {
	if t.b.MaxSteps <= 0 {
		return 0
	}
	r := t.b.MaxSteps - t.b.UsedSteps
	if r < 0 {
		return 0
	}
	return r
}

// RemainingTokens returns how many tokens remain (0 means unlimited).
func (t *BudgetTracker) RemainingTokens() int {
	if t.b.MaxTokens <= 0 {
		return 0
	}
	r := t.b.MaxTokens - t.b.UsedTokens
	if r < 0 {
		return 0
	}
	return r
}

// RemainingCost returns how much cost remains (0 means unlimited).
func (t *BudgetTracker) RemainingCost() float64 {
	if t.b.MaxCostUSD <= 0 {
		return 0
	}
	r := t.b.MaxCostUSD - t.b.UsedCostUSD
	if r < 0 {
		return 0
	}
	return r
}

// DisableTokenLimit clears the token cap while preserving usage counters.
func (t *BudgetTracker) DisableTokenLimit() {
	if t == nil || t.b == nil {
		return
	}
	t.b.MaxTokens = 0
}

// DisableStepLimit clears the step cap while preserving usage counters.
func (t *BudgetTracker) DisableStepLimit() {
	if t == nil || t.b == nil {
		return
	}
	t.b.MaxSteps = 0
}

// DisableCostLimit clears the cost cap while preserving usage counters.
func (t *BudgetTracker) DisableCostLimit() {
	if t == nil || t.b == nil {
		return
	}
	t.b.MaxCostUSD = 0
}

// Summary returns a human-readable budget usage string.
func (t *BudgetTracker) Summary() string {
	b := t.b
	s := fmt.Sprintf("steps=%d", b.UsedSteps)
	if b.MaxSteps > 0 {
		s += fmt.Sprintf("/%d", b.MaxSteps)
	}
	s += fmt.Sprintf(" tokens=%d", b.UsedTokens)
	if b.MaxTokens > 0 {
		s += fmt.Sprintf("/%d", b.MaxTokens)
	}
	s += fmt.Sprintf(" cost=$%.4f", b.UsedCostUSD)
	if b.MaxCostUSD > 0 {
		s += fmt.Sprintf("/$%.4f", b.MaxCostUSD)
	}
	return s
}

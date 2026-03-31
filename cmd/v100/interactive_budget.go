package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

type interactiveBudgetDecision int

const (
	interactiveBudgetUnhandled interactiveBudgetDecision = iota
	interactiveBudgetRetry
	interactiveBudgetContinue
)

func handleInteractiveBudgetExceeded(budget *core.BudgetTracker, budgetErr *core.ErrBudgetExceeded, prompt func(string) bool) interactiveBudgetDecision {
	if budgetErr == nil || prompt == nil || budget == nil {
		return interactiveBudgetUnhandled
	}
	switch interactiveBudgetKind(budgetErr.Reason) {
	case "token", "step", "cost":
	default:
		return interactiveBudgetUnhandled
	}
	if !prompt(budgetErr.Reason) {
		return interactiveBudgetUnhandled
	}

	switch interactiveBudgetKind(budgetErr.Reason) {
	case "token":
		budget.DisableTokenLimit()
	case "step":
		budget.DisableStepLimit()
	case "cost":
		budget.DisableCostLimit()
	}
	if interactiveBudgetShouldRetry(budgetErr.Reason) {
		return interactiveBudgetRetry
	}
	return interactiveBudgetContinue
}

func interactiveBudgetKind(reason string) string {
	switch {
	case strings.HasPrefix(reason, "tokens"):
		return "token"
	case strings.HasPrefix(reason, "steps"):
		return "step"
	case strings.HasPrefix(reason, "cost"):
		return "cost"
	default:
		return ""
	}
}

func interactiveBudgetShouldRetry(reason string) bool {
	return strings.HasPrefix(reason, "tokens nearly exhausted:") || strings.HasPrefix(reason, "steps exhausted:")
}

func interactiveBudgetLabel(reason string) string {
	switch interactiveBudgetKind(reason) {
	case "token":
		return "token budget"
	case "step":
		return "step budget"
	case "cost":
		return "cost budget"
	default:
		return "budget"
	}
}

func interactiveBudgetLimit(reason string) string {
	switch interactiveBudgetKind(reason) {
	case "token":
		return "token limit"
	case "step":
		return "step limit"
	case "cost":
		return "cost limit"
	default:
		return "budget limit"
	}
}

func interactiveBudgetConfirmMessage(reason string) string {
	label := interactiveBudgetLabel(reason)
	if label == "" {
		label = "budget"
	}
	return strings.ToUpper(label[:1]) + label[1:] + " hit (" + reason + "). Continue this interactive run without the " + interactiveBudgetLimit(reason) + "?"
}

func promptContinueWithoutBudgetLimit(in io.Reader, out io.Writer, reason string) bool {
	if in == nil || out == nil {
		return false
	}
	_, _ = fmt.Fprintf(out, "%s Continue this interactive run without the %s? [y/N]: ", ui.Warn(interactiveBudgetLabel(reason)+" hit: "+reason), interactiveBudgetLimit(reason))
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		_, _ = fmt.Fprintln(out)
		return false
	}
	_, _ = fmt.Fprintln(out)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "c", "continue":
		return true
	default:
		return false
	}
}

func runInteractiveStep(step func() error, budget *core.BudgetTracker, prompt func(string) bool) error {
	for {
		err := step()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		var budgetErr *core.ErrBudgetExceeded
		if errors.As(err, &budgetErr) {
			switch handleInteractiveBudgetExceeded(budget, budgetErr, prompt) {
			case interactiveBudgetRetry:
				continue
			case interactiveBudgetContinue:
				return nil
			}
		}
		return err
	}
}

package main

import (
	"bufio"
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
	if budgetErr == nil || prompt == nil {
		return interactiveBudgetUnhandled
	}
	if !strings.HasPrefix(budgetErr.Reason, "tokens") {
		return interactiveBudgetUnhandled
	}
	if !prompt(budgetErr.Reason) {
		return interactiveBudgetUnhandled
	}

	budget.DisableTokenLimit()
	if strings.HasPrefix(budgetErr.Reason, "tokens nearly exhausted:") {
		return interactiveBudgetRetry
	}
	return interactiveBudgetContinue
}

func promptContinueWithoutTokenLimit(in io.Reader, out io.Writer, reason string) bool {
	if in == nil || out == nil {
		return false
	}
	fmt.Fprintf(out, "%s Continue this interactive run without a token limit? [y/N]: ", ui.Warn("token budget hit: "+reason))
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		fmt.Fprintln(out)
		return false
	}
	fmt.Fprintln(out)
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "c", "continue":
		return true
	default:
		return false
	}
}

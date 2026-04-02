package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/eval"
)

func TestBindMutationBudgetFlagsDefaultsAndOverrides(t *testing.T) {
	var budgets eval.MutationBudgets
	cmd := &cobra.Command{Use: "test"}
	bindMutationBudgetFlags(cmd, &budgets)

	defaults := eval.DefaultMutationBudgets()
	if budgets != defaults {
		t.Fatalf("budgets = %+v, want %+v", budgets, defaults)
	}
	if err := cmd.ParseFlags([]string{"--max-prompt-growth-chars=123", "--max-tool-description-chars=456"}); err != nil {
		t.Fatal(err)
	}
	if budgets.MaxPromptGrowthChars != 123 {
		t.Fatalf("MaxPromptGrowthChars = %d, want 123", budgets.MaxPromptGrowthChars)
	}
	if budgets.MaxToolDescriptionChars != 456 {
		t.Fatalf("MaxToolDescriptionChars = %d, want 456", budgets.MaxToolDescriptionChars)
	}
}

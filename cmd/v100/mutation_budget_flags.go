package main

import (
	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/eval"
)

func bindMutationBudgetFlags(cmd *cobra.Command, budgets *eval.MutationBudgets) {
	defaults := eval.DefaultMutationBudgets()
	budgets.MaxPromptChars = defaults.MaxPromptChars
	budgets.MaxPromptGrowthChars = defaults.MaxPromptGrowthChars
	budgets.MaxToolDescriptionChars = defaults.MaxToolDescriptionChars
	budgets.MaxToolDescriptionGrowthChars = defaults.MaxToolDescriptionGrowthChars

	cmd.Flags().IntVar(&budgets.MaxPromptChars, "max-prompt-chars", budgets.MaxPromptChars, "max mutated prompt or policy size in chars")
	cmd.Flags().IntVar(&budgets.MaxPromptGrowthChars, "max-prompt-growth-chars", budgets.MaxPromptGrowthChars, "max mutated prompt or policy growth in chars over original")
	cmd.Flags().IntVar(&budgets.MaxToolDescriptionChars, "max-tool-description-chars", budgets.MaxToolDescriptionChars, "max mutated tool description size in chars")
	cmd.Flags().IntVar(&budgets.MaxToolDescriptionGrowthChars, "max-tool-description-growth-chars", budgets.MaxToolDescriptionGrowthChars, "max mutated tool description growth in chars over original")
}

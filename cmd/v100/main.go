package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var cfgPath string

	root := &cobra.Command{
		Use:   "v100",
		Short: "Modular CLI/TUI agent harness",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: ~/.config/v100/config.toml)")

	root.AddCommand(
		runCmd(&cfgPath),
		resumeCmd(&cfgPath),
		replayCmd(&cfgPath),
		toolsCmd(&cfgPath),
		providersCmd(&cfgPath),
		configInitCmd(),
		doctorCmd(&cfgPath),
		loginCmd(),
		logoutCmd(),
		devCmd(),
		scoreCmd(),
		statsCmd(),
		metricsCmd(),
		compareCmd(),
		benchCmd(&cfgPath),
		experimentCmd(&cfgPath),
		analyzeCmd(),
		diffCmd(),
		queryCmd(),
	)
	return root
}

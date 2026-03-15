package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var cfgPath string

	root := &cobra.Command{
		Use:     "v100",
		Short:   "Modular CLI/TUI agent harness",
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: ~/.config/v100/config.toml)")

	root.AddCommand(
		runCmd(&cfgPath),
		resumeCmd(&cfgPath),
		restoreCmd(&cfgPath),
		replayCmd(&cfgPath),
		toolsCmd(&cfgPath),
		providersCmd(&cfgPath),
		configInitCmd(),
		doctorCmd(&cfgPath),
		installCmd(),
		loginCmd(),
		logoutCmd(),
		devCmd(),
		exportCmd(),
		scoreCmd(),
		distillCmd(),
		statsCmd(),
		metricsCmd(),
		digestCmd(),
		compareCmd(),
		benchCmd(&cfgPath),
		experimentCmd(&cfgPath),
		analyzeCmd(),
		mutateCmd(&cfgPath),
		evalCmd(&cfgPath),
		diffCmd(),
		verifyCmd(),
		queryCmd(),
		runsCmd(),
	)
	return root
}

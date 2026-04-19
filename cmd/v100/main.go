package main

import (
	"fmt"
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
		Short:   "Engine for agentic research",
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Don't check for updates if we are actually running the update command itself.
			if cmd.Name() != "update" {
				checkForUpdateInBackground(cmd.Context())
			}
			return nil
		},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default: ~/.config/v100/config.toml)")

	root.AddCommand(
		runCmd(&cfgPath),
		resumeCmd(&cfgPath),
		restoreCmd(&cfgPath),
		replayCmd(&cfgPath),
		blameCmd(),
		wakeCmd(&cfgPath),
		toolsCmd(&cfgPath),
		providersCmd(&cfgPath),
		agentsCmd(&cfgPath),
		memoryCmd(&cfgPath),
		updateCmd(),
		versionCmd(),
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
		runsCmd(&cfgPath),
		evolveCmd(&cfgPath),
		researchCmd(&cfgPath),
		compressCmd(&cfgPath),
		dogfoodCmd(&cfgPath),
	)
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of v100",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("v100 %s\n", version)
		},
	}
}

//go:build windows
// +build windows

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func wakeCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Wake daemon (not available on Windows)",
		Long:  "The wake daemon is currently only supported on Linux hosts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		wakeStartCmd(cfgPath),
		wakeStatusCmd(cfgPath),
		wakeStopCmd(cfgPath),
	)

	return cmd
}

func wakeStartCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the wake daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("wake daemon is not yet supported on Windows")
		},
	}
}

func wakeStopCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the wake daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("wake daemon is not supported on Windows")
		},
	}
}

func wakeStatusCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show wake daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("wake  unsupported  (not available on Windows)")
			return nil
		},
	}
}

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/ui"
	"github.com/tripledoublev/v100/internal/update"
)

func updateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for and install the latest version from GitHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fmt.Println(ui.Info("Checking for updates..."))

			release, err := update.CheckLatest(ctx)
			if err != nil {
				return fmt.Errorf("failed to check for updates: %w", err)
			}

			if !update.IsNewer(version, release.TagName) && !force {
				fmt.Printf(ui.Info("Already up to date (current: %s, latest: %s)\n"), version, release.TagName)
				return nil
			}

			fmt.Printf(ui.Info("New version available: %s (current: %s)\n"), release.TagName, version)

			targetAsset := update.TargetAsset()
			var downloadURL string
			for _, asset := range release.Assets {
				if asset.Name == targetAsset {
					downloadURL = asset.BrowserDownloadURL
					break
				}
			}

			if downloadURL == "" {
				return fmt.Errorf("no suitable asset found for platform %s", targetAsset)
			}

			fmt.Printf(ui.Info("Downloading update from %s ...\n"), downloadURL)
			tmpPath, err := update.DownloadAsset(ctx, downloadURL)
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
			defer func() { _ = os.Remove(tmpPath) }() // ignore error, temp file cleanup

			fmt.Println(ui.Info("Applying update..."))
			if err := update.ApplyUpdate(tmpPath); err != nil {
				return fmt.Errorf("failed to apply update: %w", err)
			}

			fmt.Println(ui.OK("Successfully updated to " + release.TagName))
			fmt.Println(ui.Info("Please restart v100 to use the new version."))
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "force update even if current version is up to date")

	return cmd
}

func checkForUpdateInBackground(ctx context.Context) {
	// This is a simplified version for now. 
	// In a real implementation, we would check a timestamp to avoid hitting the API every time.
	go func() {
		release, err := update.CheckLatest(ctx)
		if err != nil {
			return
		}
		if update.IsNewer(version, release.TagName) {
			fmt.Fprintf(os.Stderr, "\n%s\n", ui.Warn(fmt.Sprintf("New version %s is available! Run 'v100 update' to install.", release.TagName)))
		}
	}()
}

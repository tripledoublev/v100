package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func installCmd() *cobra.Command {
	var destFlag string

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Link the shell-resolved v100 binary to this build",
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := strings.TrimSpace(destFlag)
			if dest == "" {
				var err error
				dest, err = defaultInstallPath()
				if err != nil {
					return err
				}
			}

			source, err := discoverInstallSource()
			if err != nil {
				return err
			}

			dest, err = filepath.Abs(dest)
			if err != nil {
				return fmt.Errorf("resolve install destination: %w", err)
			}

			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return fmt.Errorf("create install directory: %w", err)
			}
			if err := os.RemoveAll(dest); err != nil {
				return fmt.Errorf("remove existing destination: %w", err)
			}
			if err := os.Symlink(source, dest); err != nil {
				return fmt.Errorf("create symlink %s -> %s: %w", dest, source, err)
			}

			fmt.Printf("linked %s -> %s\n", dest, source)
			return nil
		},
	}

	cmd.Flags().StringVar(&destFlag, "dest", "", "destination path for the v100 symlink (default: ~/.local/bin/v100)")
	return cmd
}

func defaultInstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin", "v100"), nil
}

func discoverInstallSource() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	if source, ok := repoBuildBinary(cwd); ok {
		return source, nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	source, err := filepath.EvalSymlinks(exePath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("resolve executable symlink: %w", err)
	}
	if err == nil {
		exePath = source
	}
	return filepath.Abs(exePath)
}

func repoBuildBinary(root string) (string, bool) {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "v100", "main.go")); err != nil {
		return "", false
	}
	binary := filepath.Join(root, "v100")
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() {
		return "", false
	}
	source, err := filepath.Abs(binary)
	if err != nil {
		return "", false
	}
	return source, true
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func devCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dev [args...]",
		Short: "Dev mode: rebuild+restart on demand (create .v100-reload to trigger)",
		RunE:  runDev,
	}
}

// sentinelFile is the path (relative to project root) the agent or user creates
// to request a rebuild+restart at a strategic moment.
const sentinelFile = ".v100-reload"

func runDev(cmd *cobra.Command, args []string) error {
	root, err := findProjectRoot()
	if err != nil {
		return fmt.Errorf("cannot find project root: %w", err)
	}

	childArgs := args
	if len(childArgs) == 0 {
		childArgs = []string{"run", "--tui"}
	}

	binary := filepath.Join(root, "v100")
	sentinel := filepath.Join(root, sentinelFile)

	fmt.Printf("→ reload trigger: create %s\n", sentinel)

	for {
		fmt.Println("→ building…")
		if err := buildBinary(root, binary); err != nil {
			fmt.Fprintf(os.Stderr, "build failed: %v\n", err)
			waitForSentinel(sentinel)
			_ = os.Remove(sentinel)
			continue
		}

		fmt.Println("→ starting v100")
		child := exec.Command(binary, childArgs...)
		child.Stdin = os.Stdin
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr

		if err := child.Start(); err != nil {
			return fmt.Errorf("start failed: %w", err)
		}

		reloadReq := make(chan struct{}, 1)
		stopWatch := make(chan struct{})
		go watchSentinelChan(sentinel, reloadReq, stopWatch)

		childDone := make(chan error, 1)
		go func() { childDone <- child.Wait() }()

		select {
		case <-reloadReq:
			close(stopWatch)
			_ = os.Remove(sentinel)

			// SIGINT lets bubbletea restore terminal (alt-screen, raw mode).
			_ = child.Process.Signal(os.Interrupt)
			select {
			case <-childDone:
			case <-time.After(2 * time.Second):
				_ = child.Process.Kill()
				<-childDone
			}
			// Safety net: restore terminal in case TUI cleanup didn't run.
			_ = exec.Command("stty", "sane").Run()
			fmt.Println("→ reload triggered, rebuilding…")

		case exitErr := <-childDone:
			close(stopWatch)
			if exitErr != nil {
				// Crash — rebuild and restart immediately.
				fmt.Fprintf(os.Stderr, "→ child crashed: %v — rebuilding…\n", exitErr)
				continue
			}
			// Clean exit (user /quit) — supervisor exits too.
			return nil
		}
	}
}

func buildBinary(root, binary string) error {
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/v100/")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// watchSentinelChan polls for the sentinel file and signals reloadReq when found.
func watchSentinelChan(sentinel string, reloadReq chan<- struct{}, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-time.After(500 * time.Millisecond):
			if _, err := os.Stat(sentinel); err == nil {
				select {
				case reloadReq <- struct{}{}:
				default:
				}
			}
		}
	}
}

// waitForSentinel blocks until the sentinel file appears.
func waitForSentinel(sentinel string) {
	for {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(sentinel); err == nil {
			return
		}
	}
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found")
}

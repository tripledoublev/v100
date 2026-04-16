package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

// CompressCheckpoint is written to compress.checkpoint.json in the run dir
// so that `v100 resume` can start from the compressed message history.
type CompressCheckpoint struct {
	Messages []providers.Message `json:"messages"`
}

func compressCmd(cfgPath *string) *cobra.Command {
	var (
		providerFlag string
		dryRunFlag   bool
	)

	cmd := &cobra.Command{
		Use:          "compress <run_id>",
		Short:        "Force-compress the message history of an existing run",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]

			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			tracePath := filepath.Join(runDir, "trace.jsonl")
			events, err := core.ReadAll(tracePath)
			if err != nil {
				return fmt.Errorf("read trace: %w", err)
			}

			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}

			msgs, _, _, _, _ := reconstructHistory(runDir, events)
			if len(msgs) == 0 {
				return fmt.Errorf("no messages found in trace %s", runID)
			}

			// Resolve compress provider: --provider flag > config compress_provider > auto-select
			var cp providers.Provider
			if providerFlag != "" {
				cp, err = buildProvider(cfg, providerFlag)
				if err != nil {
					return fmt.Errorf("build compress provider %q: %w", providerFlag, err)
				}
			} else {
				cp = buildCompressProvider(cfg)
				if cp == nil {
					// Fall back to default provider
					defaultName := cfg.Defaults.Provider
					if defaultName == "" {
						return fmt.Errorf("no compress provider configured; use --provider to specify one")
					}
					cp, err = buildProvider(cfg, defaultName)
					if err != nil {
						return fmt.Errorf("build default provider %q: %w", defaultName, err)
					}
				}
			}

			pol := loadPolicy(cfg, "default")

			tokensBefore := estimateTokensSlice(msgs)
			fmt.Fprintf(os.Stderr, "compressing run %s — %d messages, ~%d tokens\n", runID, len(msgs), tokensBefore)

			if dryRunFlag {
				fmt.Printf("messages:     %d\n", len(msgs))
				fmt.Printf("tokens_est:   %d\n", tokensBefore)
				fmt.Printf("provider:     %s\n", cp.Name())
				fmt.Println("(dry-run: no changes written)")
				return nil
			}

			run := &core.Run{ID: runID, Dir: runDir, TraceFile: tracePath}
			loop := &core.Loop{
				Run:              run,
				CompressProvider: cp,
				Provider:         cp, // compress calls use cp; no main inference needed
				Policy:           pol,
				Budget:           core.NewBudgetTracker(&core.Budget{}),
				Messages:         msgs,
				OutputFn:         func(ev core.Event) {},
			}

			ctx := context.Background()
			if err := loop.ForceCompress(ctx, "compress"); err != nil {
				return fmt.Errorf("compression failed: %w", err)
			}

			tokensAfter := estimateTokensSlice(loop.Messages)
			saved := tokensBefore - tokensAfter
			pct := 0
			if tokensBefore > 0 {
				pct = saved * 100 / tokensBefore
			}

			fmt.Printf("messages:     %d → %d\n", len(msgs), len(loop.Messages))
			fmt.Printf("tokens_est:   %d → %d  (saved %d, %d%%)\n", tokensBefore, tokensAfter, saved, pct)
			fmt.Printf("provider:     %s\n", cp.Name())

			// Write checkpoint for `v100 resume` to pick up
			checkpointPath := filepath.Join(runDir, "compress.checkpoint.json")
			data, err := json.MarshalIndent(CompressCheckpoint{Messages: loop.Messages}, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal checkpoint: %w", err)
			}
			if err := os.WriteFile(checkpointPath, data, 0o644); err != nil {
				return fmt.Errorf("write checkpoint: %w", err)
			}
			fmt.Printf("checkpoint:   %s\n", checkpointPath)
			fmt.Println("Run `v100 resume " + runID + "` to continue from compressed context.")
			return nil
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "provider to use for compression (overrides config compress_provider)")
	cmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "print stats without compressing or writing checkpoint")
	return cmd
}

// estimateTokensSlice is a package-level wrapper so cmd_compress.go can call
// the unexported estimateTokens in internal/core without import tricks.
// We inline the same formula here to avoid coupling.
func estimateTokensSlice(msgs []providers.Message) int {
	n := 0
	for _, m := range msgs {
		n += 4 + len(m.Content)*10/33 + 1
		for _, tc := range m.ToolCalls {
			n += 10 + len(tc.Args)*10/33 + 1
		}
	}
	return n
}

// loadCheckpoint loads a compress.checkpoint.json from the run dir if it exists.
// Returns nil, nil if no checkpoint is present.
func loadCheckpoint(runDir string) ([]providers.Message, error) {
	p := filepath.Join(runDir, "compress.checkpoint.json")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read compress checkpoint: %w", err)
	}
	var ck CompressCheckpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		return nil, fmt.Errorf("parse compress checkpoint: %w", err)
	}
	return ck.Messages, nil
}


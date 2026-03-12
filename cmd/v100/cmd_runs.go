package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func runsCmd() *cobra.Command {
	var limit int
	var runDir string
	var allFlag bool
	var providerFilter string
	var failedFlag bool

	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List recent runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runDir == "" {
				runDir = "runs"
			}
			entries, err := os.ReadDir(runDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println(ui.Dim("No runs found"))
					return nil
				}
				return err
			}

			// Filter to directories only, sort newest first
			type runEntry struct {
				name    string
				modTime time.Time
			}
			var dirs []runEntry
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				dirs = append(dirs, runEntry{name: e.Name(), modTime: info.ModTime()})
			}
			sort.Slice(dirs, func(i, j int) bool {
				return dirs[i].modTime.After(dirs[j].modTime)
			})

			if limit > 0 && len(dirs) > limit {
				dirs = dirs[:limit]
			}

			for _, d := range dirs {
				dir := filepath.Join(runDir, d.name)
				meta, _ := core.ReadMeta(dir)

				// Filter: skip sub-runs unless --all
				if !allFlag && meta.ParentRunID != "" {
					continue
				}
				// Filter: --provider
				if providerFilter != "" && meta.Provider != providerFilter {
					continue
				}
				// Filter: --failed (end_reason != completed or score == fail)
				if failedFlag {
					events, _ := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
					stats := core.ComputeStats(events)
					if stats.EndReason == "completed" && meta.Score != "fail" {
						continue
					}
				}

				name := strings.TrimSpace(meta.Name)
				provider := strings.TrimSpace(meta.Provider)
				model := strings.TrimSpace(meta.Model)

				label := d.name
				if meta.ParentRunID != "" {
					label = "  ↳ " + d.name
				}
				parts := []string{}
				if provider != "" {
					parts = append(parts, provider)
				}
				if model != "" {
					parts = append(parts, model)
				}
				if name != "" {
					parts = append(parts, ui.Bold(name))
				}

				if len(parts) > 0 {
					label += "  " + strings.Join(parts, " · ")
				}

				// Extract first user prompt from trace
				if prompt := firstUserPrompt(dir); prompt != "" {
					label += "\n    " + ui.Dim(prompt)
				}
				fmt.Println(label)
			}

			if len(dirs) == 0 {
				fmt.Println(ui.Dim("No runs found"))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "max runs to show")
	cmd.Flags().StringVar(&runDir, "run-dir", "", "runs directory (default: ./runs)")
	cmd.Flags().BoolVar(&allFlag, "all", false, "show sub-runs and all entries")
	cmd.Flags().StringVar(&providerFilter, "provider", "", "filter by provider name")
	cmd.Flags().BoolVar(&failedFlag, "failed", false, "show only failed/errored runs")
	return cmd
}

// firstUserPrompt reads the trace and returns the first user message, truncated.
func firstUserPrompt(dir string) string {
	events, err := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
	if err != nil {
		return ""
	}
	for _, ev := range events {
		if ev.Type != core.EventUserMsg {
			continue
		}
		var p core.UserMsgPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			continue
		}
		prompt := strings.TrimSpace(p.Content)
		// Collapse to single line
		prompt = strings.Join(strings.Fields(prompt), " ")
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}
		return prompt
	}
	return ""
}

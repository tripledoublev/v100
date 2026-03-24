package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/memory"
)

func memoryCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Review durable memory entries in the current workspace",
	}
	cmd.AddCommand(
		memoryListCmd(cfgPath),
		memoryShowCmd(cfgPath),
	)
	return cmd
}

func memoryListCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List durable memory entries for the current workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace, err := os.Getwd()
			if err != nil {
				return err
			}
			entries, err := memory.LoadAuditEntries(workspace)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("No durable memory entries found in this workspace.")
				return nil
			}
			for _, entry := range entries {
				printAuditEntry(entry)
			}
			return nil
		},
	}
}

func memoryShowCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one durable memory entry by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace, err := os.Getwd()
			if err != nil {
				return err
			}
			entries, err := memory.LoadAuditEntries(workspace)
			if err != nil {
				return err
			}
			id := strings.TrimSpace(args[0])
			for _, entry := range entries {
				if entry.ID == id {
					printAuditEntry(entry)
					return nil
				}
			}
			return fmt.Errorf("memory entry %q not found in %s", id, filepath.Base(workspace))
		},
	}
}

func printAuditEntry(entry memory.AuditEntry) {
	meta := []string{
		"id=" + entry.ID,
		"source=" + entry.Source,
		"scope=" + entry.Scope,
		"origin=" + entry.Origin,
		"confidence=" + entry.Confidence,
	}
	if entry.Provenance != "" {
		meta = append(meta, "provenance="+entry.Provenance)
	}
	if !entry.Timestamp.IsZero() {
		meta = append(meta, "ts="+entry.Timestamp.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if len(entry.Tags) > 0 {
		meta = append(meta, "tags="+formatAuditTags(entry.Tags))
	}
	fmt.Println(strings.Join(meta, " "))
	fmt.Println("  " + entry.Content)
}

func formatAuditTags(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+tags[key])
	}
	return strings.Join(parts, ",")
}

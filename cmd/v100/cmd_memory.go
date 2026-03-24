package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/memory"
	"github.com/tripledoublev/v100/internal/providers"
)

var buildMemoryProvider = buildProvider

func memoryCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Review durable memory entries in the current workspace",
	}
	cmd.AddCommand(
		memoryListCmd(cfgPath),
		memoryRememberCmd(cfgPath),
		memoryForgetCmd(cfgPath),
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

func memoryRememberCmd(cfgPath *string) *cobra.Command {
	var tagFlags []string
	cmd := &cobra.Command{
		Use:   "remember <text>",
		Short: "Store a durable memory entry in the current workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			prov, err := buildMemoryProvider(cfg, cfg.Defaults.Provider)
			if err != nil {
				return err
			}
			workspace, err := os.Getwd()
			if err != nil {
				return err
			}
			content := strings.TrimSpace(args[0])
			emb, err := prov.Embed(cmd.Context(), providers.EmbedRequest{Text: content})
			if err != nil {
				return err
			}
			store := memory.NewWorkspaceVectorStore(workspace)
			_ = store.Load()
			tags := parseTags(tagFlags)
			if _, ok := tags["scope"]; !ok {
				tags["scope"] = "workspace"
			}
			if _, ok := tags["origin"]; !ok {
				tags["origin"] = "cli:remember"
			}
			if _, ok := tags["confidence"]; !ok {
				tags["confidence"] = "manual"
			}
			item := memory.MemoryItem{
				ID:        fmt.Sprintf("mem-%x", time.Now().UnixNano()),
				Content:   content,
				Embedding: emb.Embedding,
				Metadata:  memory.Metadata{Tags: tags},
				TS:        time.Now().UTC(),
			}
			if err := store.Add(item); err != nil {
				return err
			}
			fmt.Println("stored memory entry: " + item.ID)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "memory tag in key=value form")
	return cmd
}

func memoryForgetCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <id>",
		Short: "Remove a durable memory entry by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace, err := os.Getwd()
			if err != nil {
				return err
			}
			id := strings.TrimSpace(args[0])
			if err := memory.ForgetAuditEntry(workspace, id); err != nil {
				return err
			}
			fmt.Println("forgot memory entry: " + id)
			return nil
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

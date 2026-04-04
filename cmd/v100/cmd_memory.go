package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	var categoryFilter string
	cmd := &cobra.Command{
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
			if categoryFilter != "" {
				filtered := entries[:0]
				for _, e := range entries {
					if strings.EqualFold(e.Category, categoryFilter) {
						filtered = append(filtered, e)
					}
				}
				entries = filtered
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
	cmd.Flags().StringVar(&categoryFilter, "category", "", "filter by category (fact, preference, constraint, note)")
	return cmd
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
	var category string
	var expires string
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

			var expiresAt *time.Time
			if expires != "" {
				d, parseErr := parseDuration(expires)
				if parseErr != nil {
					return fmt.Errorf("invalid --expires value %q: %w", expires, parseErr)
				}
				t := time.Now().UTC().Add(d)
				expiresAt = &t
			}

			item := memory.MemoryItem{
				ID:        fmt.Sprintf("mem-%x", time.Now().UnixNano()),
				Content:   content,
				Category:  category,
				Embedding: emb.Embedding,
				Metadata:  memory.Metadata{Tags: tags},
				TS:        time.Now().UTC(),
				ExpiresAt: expiresAt,
			}
			if err := store.Add(item); err != nil {
				return err
			}
			label := "stored memory entry: " + item.ID
			if category != "" {
				label += " [" + category + "]"
			}
			if expiresAt != nil {
				label += " (expires " + expiresAt.Format(time.RFC3339) + ")"
			}
			fmt.Println(label)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "memory tag in key=value form")
	cmd.Flags().StringVar(&category, "category", "", "memory category (fact, preference, constraint, note)")
	cmd.Flags().StringVar(&expires, "expires", "", "TTL duration (e.g. 1h, 7d, 30m)")
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
	}
	if entry.Category != "" {
		meta = append(meta, "category="+entry.Category)
	}
	meta = append(meta,
		"source="+entry.Source,
		"scope="+entry.Scope,
		"origin="+entry.Origin,
		"confidence="+entry.Confidence,
	)
	if entry.Provenance != "" {
		meta = append(meta, "provenance="+entry.Provenance)
	}
	if !entry.Timestamp.IsZero() {
		meta = append(meta, "ts="+entry.Timestamp.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if entry.ExpiresAt != nil {
		meta = append(meta, "expires="+entry.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if len(entry.Tags) > 0 {
		meta = append(meta, "tags="+formatAuditTags(entry.Tags))
	}
	fmt.Println(strings.Join(meta, " "))
	fmt.Println("  " + entry.Content)
}

// parseDuration parses a duration string supporting "d" for days in addition
// to standard Go durations (h, m, s).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(strings.ToLower(s), "d") {
		daysText := strings.TrimSuffix(strings.ToLower(s), "d")
		if daysText == "" {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		days, err := strconv.Atoi(daysText)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
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

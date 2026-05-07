package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func lexiconCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lexicon",
		Short: "Publish and verify custom ATProto lexicons",
	}
	cmd.AddCommand(publishProvenanceLexiconCmd(cfgPath))
	return cmd
}

func publishProvenanceLexiconCmd(cfgPath *string) *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:   "publish-provenance",
		Short: "Publish art.xx-c.provenance as a com.atproto.lexicon.schema record",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(*cfgPath)
			if err != nil {
				return err
			}
			record := provenanceLexiconRecord()
			validate := false
			payload, _ := json.Marshal(map[string]any{
				"collection": "com.atproto.lexicon.schema",
				"rkey":       "art.xx-c.provenance",
				"account":    account,
				"record":     record,
				"validate":   validate,
			})
			result, err := tools.ATProtoPutRecord(cfg).Exec(context.Background(), tools.ToolCallContext{}, payload)
			if err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("%s", result.Output)
			}
			fmt.Println(ui.Info("published provenance lexicon: " + result.Output))
			repoDID := publishedRecordRepoDID(result.Output)
			if err := verifyLexiconDNS("xx-c.art", repoDID); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&account, "account", "alt", "account to publish from: main or alt")
	return cmd
}

func provenanceLexiconRecord() map[string]any {
	return map[string]any{
		"$type":       "com.atproto.lexicon.schema",
		"lexicon":     1,
		"id":          "art.xx-c.provenance",
		"description": "Provenance for v100 autonomous synthesis.",
		"defs": map[string]any{
			"main": map[string]any{
				"type":        "record",
				"key":         "tid",
				"description": "Provenance for v100 autonomous synthesis.",
				"record": map[string]any{
					"type":     "object",
					"required": []string{"subject", "sources", "createdAt"},
					"properties": map[string]any{
						"subject": map[string]any{
							"type":        "ref",
							"ref":         "com.atproto.repo.strongRef",
							"description": "Strong reference to the synthesis post this provenance record describes.",
						},
						"sources": map[string]any{
							"type":        "array",
							"description": "Strong references to source records used for the synthesis.",
							"items": map[string]any{
								"type": "ref",
								"ref":  "com.atproto.repo.strongRef",
							},
						},
						"agent": map[string]any{
							"type":        "string",
							"description": "v100 version/run ID",
						},
						"createdAt": map[string]any{
							"type":   "string",
							"format": "datetime",
						},
					},
				},
			},
		},
	}
}

func publishedRecordRepoDID(output string) string {
	var payload struct {
		Repo string `json:"repo"`
	}
	if json.Unmarshal([]byte(output), &payload) == nil {
		return strings.TrimSpace(payload.Repo)
	}
	return ""
}

func verifyLexiconDNS(domain, wantDID string) error {
	records, err := net.LookupTXT("_lexicon." + domain)
	if err != nil {
		return fmt.Errorf("lexicon DNS not verified: %w", err)
	}
	for _, record := range records {
		record = strings.TrimSpace(record)
		if wantDID != "" && (record == "did="+wantDID || record == wantDID) {
			fmt.Println(ui.Info("lexicon DNS: " + record))
			return nil
		}
		if wantDID == "" && (strings.Contains(record, "did=") || strings.Contains(record, "did:")) {
			fmt.Println(ui.Warn("lexicon DNS has DID pointer, but published repo DID could not be checked: " + record))
			return nil
		}
	}
	if wantDID != "" {
		return fmt.Errorf("lexicon DNS mismatch: _lexicon.%s must point to did=%s (records: %s)", domain, wantDID, strings.Join(records, "; "))
	}
	return fmt.Errorf("lexicon DNS records found, but no DID pointer was detected")
}

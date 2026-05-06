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
			payload, _ := json.Marshal(map[string]any{
				"collection": "com.atproto.lexicon.schema",
				"rkey":       "art.xx-c.provenance",
				"account":    account,
				"record":     record,
			})
			result, err := tools.ATProtoCreateRecord(cfg).Exec(context.Background(), tools.ToolCallContext{}, payload)
			if err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("%s", result.Output)
			}
			fmt.Println(ui.Info("published provenance lexicon: " + result.Output))
			verifyLexiconDNS("xx-c.art")
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
					"required": []string{"post", "sources", "createdAt"},
					"properties": map[string]any{
						"post": map[string]any{
							"type":        "string",
							"format":      "at-uri",
							"description": "URI of the synthesis post.",
						},
						"sources": map[string]any{
							"type":        "array",
							"description": "Source URIs used for the synthesis.",
							"items": map[string]any{
								"type":   "string",
								"format": "at-uri",
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

func verifyLexiconDNS(domain string) {
	records, err := net.LookupTXT("_lexicon." + domain)
	if err != nil {
		fmt.Println(ui.Warn("lexicon DNS not verified: " + err.Error()))
		return
	}
	for _, record := range records {
		if strings.Contains(record, "did=") || strings.Contains(record, "did:") {
			fmt.Println(ui.Info("lexicon DNS: " + record))
			return
		}
	}
	fmt.Println(ui.Warn("lexicon DNS records found, but no DID pointer was detected"))
}

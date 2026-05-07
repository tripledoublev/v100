package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

// ---------------------------------------------------------------------------
// atproto_anon_synth — gather and anonymize recent feed corpus
// ---------------------------------------------------------------------------

type atrotoAnonSynthTool struct{ cfg *config.Config }

// ATProtoAnonSynth returns the atproto_anon_synth tool.
func ATProtoAnonSynth(cfg *config.Config) Tool { return &atrotoAnonSynthTool{cfg: cfg} }

func (t *atrotoAnonSynthTool) Name() string { return "atproto_anon_synth" }
func (t *atrotoAnonSynthTool) Description() string {
	return "Fetch recent Bluesky feed posts and build an anonymized text corpus. " +
		"Strips all handles, @mentions, and URLs — returns content only. " +
		"Use this to gather feed data for analysis or synthesis by a subsequent step."
}
func (t *atrotoAnonSynthTool) DangerLevel() DangerLevel { return Safe }
func (t *atrotoAnonSynthTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atrotoAnonSynthTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit":  {"type": "integer", "description": "Number of recent feed posts to fetch (default 50, max 100)."},
			"hours":  {"type": "integer", "description": "Lookback window in hours (default 12, max 72)."}
		}
	}`)
}

func (t *atrotoAnonSynthTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"corpus":     {"type": "string",  "description": "Anonymized text corpus from recent feed."},
			"sources":    {"type": "array",   "description": "Strong refs for source feed records included in the corpus, captured before anonymization.", "items": {"type": "object", "required": ["uri", "cid"], "properties": {"uri": {"type": "string", "format": "at-uri"}, "cid": {"type": "string", "format": "cid"}}}},
			"post_count": {"type": "integer", "description": "Number of posts included in corpus."},
			"skipped":    {"type": "integer", "description": "Posts excluded (outside time window or empty)."}
		}
	}`)
}

var (
	reHandle    = regexp.MustCompile(`@[\w.-]+`)
	reURL       = regexp.MustCompile(`https?://\S+`)
	reBskyLabel = regexp.MustCompile(`\bb(?:s)?ky\.app\S*`)
)

// anonymize strips handles, URLs, and bsky.app references from post text.
func anonymize(text string) string {
	text = reURL.ReplaceAllString(text, "")
	text = reBskyLabel.ReplaceAllString(text, "")
	text = reHandle.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func (t *atrotoAnonSynthTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Limit int `json:"limit"`
		Hours int `json:"hours"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 100 {
		in.Limit = 100
	}
	if in.Hours <= 0 {
		in.Hours = 12
	}
	if in.Hours > 72 {
		in.Hours = 72
	}

	// Read from main account feed.
	accountCfg, err := pickATProtoAccount(t.cfg, "main")
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	cli := newATProtoClient(accountCfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	params := url.Values{"limit": {fmt.Sprintf("%d", in.Limit)}}
	data, err := cli.xrpcGet("app.bsky.feed.getTimeline", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	var resp struct {
		Feed []struct {
			Post struct {
				URI    string `json:"uri"`
				CID    string `json:"cid"`
				Record struct {
					Text      string `json:"text"`
					CreatedAt string `json:"createdAt"`
				} `json:"record"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return ToolResult{OK: false, Output: "parse error: " + err.Error()}, nil
	}

	// Build anonymized corpus — text only, no identifiers.
	cutoff := time.Now().Add(-time.Duration(in.Hours) * time.Hour)
	var lines []string
	var sources []map[string]string
	var skipped int
	var parseFails int
	for _, item := range resp.Feed {
		raw := strings.TrimSpace(item.Post.Record.Text)
		if raw == "" {
			skipped++
			continue
		}
		// Filter to recent posts only.
		if item.Post.Record.CreatedAt != "" {
			t, err := time.Parse(time.RFC3339, item.Post.Record.CreatedAt)
			if err != nil {
				parseFails++
				continue
			}
			if t.Before(cutoff) {
				skipped++
				continue
			}
		}
		clean := anonymize(raw)
		if clean == "" {
			skipped++
			continue
		}
		lines = append(lines, clean)
		sourceURI := strings.TrimSpace(item.Post.URI)
		sourceCID := strings.TrimSpace(item.Post.CID)
		if strings.HasPrefix(sourceURI, "at://") && sourceCID != "" {
			sources = append(sources, map[string]string{"uri": sourceURI, "cid": sourceCID})
		}
	}

	if len(lines) == 0 {
		return ToolResult{OK: true, Output: fmt.Sprintf(
			"no posts in last %dh (fetched %d, skipped %d, time-parse errors %d).",
			in.Hours, len(resp.Feed), skipped, parseFails,
		)}, nil
	}

	out, _ := json.Marshal(map[string]any{
		"corpus":     strings.Join(lines, "\n"),
		"sources":    sources,
		"post_count": len(lines),
		"skipped":    skipped,
	})
	return ToolResult{OK: true, Output: string(out)}, nil
}

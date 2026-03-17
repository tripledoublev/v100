package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type webExtractTool struct{}

func WebExtract() Tool { return &webExtractTool{} }

func (t *webExtractTool) Name() string { return "web_extract" }
func (t *webExtractTool) Description() string {
	return "Fetch a web page and return compact extracted signal such as title, headings, and snippets instead of raw page text."
}
func (t *webExtractTool) DangerLevel() DangerLevel { return Safe }
func (t *webExtractTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *webExtractTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["url"],
		"properties": {
			"url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
			"max_bytes": {"type": "integer", "description": "Maximum bytes to read from response body.", "default": 131072}
		}
	}`)
}

func (t *webExtractTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string"},
			"status": {"type": "integer"},
			"content_type": {"type": "string"},
			"extract": {"type": "string"}
		}
	}`)
}

func (t *webExtractTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		URL      string `json:"url"`
		MaxBytes int64  `json:"max_bytes"`
	}
	a.MaxBytes = 128 * 1024
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	url := strings.TrimSpace(a.URL)
	if url == "" {
		return failResult(start, "url is required"), nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return failResult(start, "url must start with http:// or https://"), nil
	}
	if a.MaxBytes <= 0 || a.MaxBytes > 2*1024*1024 {
		a.MaxBytes = 128 * 1024
	}

	fetched, fail, err := fetchHTTPBody(ctx, call, start, url, a.MaxBytes)
	if err != nil {
		return ToolResult{}, err
	}
	if fail != nil {
		return *fail, nil
	}

	output := fmt.Sprintf("url: %s\nstatus: %d\ncontent_type: %s\n\n%s", url, fetched.status, fetched.contentType, fetched.text)
	return ToolResult{
		OK:         fetched.status >= 200 && fetched.status < 400,
		Output:     output,
		Stdout:     output,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

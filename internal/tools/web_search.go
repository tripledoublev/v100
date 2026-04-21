package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type webSearchTool struct{}

func WebSearch() Tool { return &webSearchTool{} }

func (t *webSearchTool) Name() string { return "web_search" }
func (t *webSearchTool) Description() string {
	return "Search the web using Brave Search. Returns a list of results with title, URL, and description for each match."
}
func (t *webSearchTool) DangerLevel() DangerLevel { return Safe }
func (t *webSearchTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *webSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["query"],
		"properties": {
			"query": {"type": "string", "description": "Search query string."},
			"count": {"type": "integer", "description": "Number of results to return (1-20, default 5).", "default": 5}
		}
	}`)
}

func (t *webSearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"results": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"title": {"type": "string"},
						"url": {"type": "string"},
						"description": {"type": "string"}
					}
				}
			}
		}
	}`)
}

func (t *webSearchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return failResult(start, "query is required"), nil
	}
	if a.Count <= 0 || a.Count > 20 {
		a.Count = 5
	}

	apiKey := os.Getenv("BRAVE_SEARCH_API_KEY")
	if apiKey == "" {
		return failResult(start, "BRAVE_SEARCH_API_KEY environment variable is not set"), nil
	}

	results, err := braveSearch(ctx, call, query, a.Count, apiKey)
	if err != nil {
		return failResult(start, "search failed: "+err.Error()), nil
	}

	var lines []string
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s\n   %s", i+1, r.Title, r.URL, r.Description))
	}

	output := fmt.Sprintf("query: %s\ncount: %d\n\n%s", query, len(results), strings.Join(lines, "\n\n"))
	return ToolResult{
		OK:         true,
		Output:     output,
		Stdout:     output,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

type braveResult struct {
	Title       string
	URL         string
	Description string
}

func braveSearch(ctx context.Context, call ToolCallContext, query string, count int, apiKey string) ([]braveResult, error) {
	timeout := 15 * time.Second
	if call.TimeoutMS > 0 {
		t := time.Duration(call.TimeoutMS) * time.Millisecond
		if t < timeout {
			timeout = t
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query),
		count,
	)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]braveResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		if r.Title == "" && r.URL == "" {
			continue
		}
		results = append(results, braveResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}
	return results, nil
}

// WebSearchEnabled returns true if the Brave Search API key is configured.
func WebSearchEnabled() bool {
	return os.Getenv("BRAVE_SEARCH_API_KEY") != ""
}

var _ Tool = (*webSearchTool)(nil)

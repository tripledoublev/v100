package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tripledoublev/v100/internal/core/executor"
)

// ---------------------------------------------------------------------------
// Wikipedia tool — local-first cache + remote fetch via MediaWiki API
// ---------------------------------------------------------------------------

type wikiTool struct{}

func Wiki() Tool { return &wikiTool{} }

func (t *wikiTool) Name() string { return "wiki" }
func (t *wikiTool) Description() string {
	return "Read Wikipedia articles with a local cache. Searches cache first, fetches from Wikipedia API on miss. Supports read, search, and update actions in multiple languages."
}
func (t *wikiTool) DangerLevel() DangerLevel { return Safe }
func (t *wikiTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *wikiTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["action"],
		"properties": {
			"action": {
				"type": "string",
				"enum": ["read", "search", "update"],
				"description": "read: fetch article by title (cache-first). search: search articles by query (always remote). update: force refresh cached article."
			},
			"title": {
				"type": "string",
				"description": "Article title (for read/update). Case-insensitive, spaces or underscores OK."
			},
			"query": {
				"type": "string",
				"description": "Search query (for search action)."
			},
			"lang": {
				"type": "string",
				"description": "Wikipedia language edition: 'en', 'fr', etc. Default: 'en'.",
				"default": "en"
			},
			"sentences": {
				"type": "integer",
				"description": "Number of sentences to return (0 = full extract). Only applies to read/update.",
				"default": 0
			},
			"max_results": {
				"type": "integer",
				"description": "Max search results (for search action). Default: 8.",
				"default": 8
			}
		}
	}`)
}

func (t *wikiTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok": {"type": "boolean"},
			"output": {"type": "string"},
			"duration_ms": {"type": "integer"}
		}
	}`)
}

func (t *wikiTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Action     string `json:"action"`
		Title      string `json:"title"`
		Query      string `json:"query"`
		Lang       string `json:"lang"`
		Sentences  int    `json:"sentences"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	if a.Lang == "" {
		a.Lang = "en"
	}
	if a.MaxResults <= 0 {
		a.MaxResults = 8
	}

	cacheDir := wikiCacheDir(call, a.Lang)

	switch a.Action {
	case "read":
		return t.doRead(ctx, call, start, a.Title, a.Lang, cacheDir, a.Sentences, false)
	case "update":
		return t.doRead(ctx, call, start, a.Title, a.Lang, cacheDir, a.Sentences, true)
	case "search":
		return t.doSearch(ctx, call, start, a.Query, a.Lang, a.MaxResults)
	default:
		return failResult(start, "unknown action: "+a.Action+", use read, search, or update"), nil
	}
}

// ---- read / update --------------------------------------------------------

func (t *wikiTool) doRead(ctx context.Context, call ToolCallContext, start time.Time, title, lang, cacheDir string, sentences int, forceUpdate bool) (ToolResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return failResult(start, "title is required for read/update"), nil
	}

	slug := wikiSlug(title)

	// Try cache first (unless force update)
	if !forceUpdate {
		cached, err := wikiReadCache(cacheDir, slug)
		if err == nil && cached != nil {
			extract := cached.Extract
			if sentences > 0 {
				extract = wikiTruncateSentences(extract, sentences)
			}
			output := fmt.Sprintf("title: %s\npageid: %d\nsource: cache\nfetched_at: %s\nlang: %s\n\n%s",
				cached.Title, cached.PageID, cached.FetchedAt.Format(time.RFC3339), cached.Lang, extract)
			return ToolResult{OK: true, Output: output, Stdout: output, DurationMS: time.Since(start).Milliseconds()}, nil
		}
	}

	// Fetch from Wikipedia API
	article, err := wikiFetchArticle(ctx, call, title, lang, sentences)
	if err != nil {
		return failResult(start, "fetch failed: "+err.Error()), nil
	}
	if article == nil {
		return failResult(start, fmt.Sprintf("article not found: %s", title)), nil
	}

	// Save to cache
	_ = wikiWriteCache(cacheDir, slug, article)

	extract := article.Extract
	if sentences > 0 {
		extract = wikiTruncateSentences(extract, sentences)
	}

	output := fmt.Sprintf("title: %s\npageid: %d\nsource: remote\nfetched_at: %s\nlang: %s\n\n%s",
		article.Title, article.PageID, article.FetchedAt.Format(time.RFC3339), article.Lang, extract)
	return ToolResult{OK: true, Output: output, Stdout: output, DurationMS: time.Since(start).Milliseconds()}, nil
}

// ---- search ---------------------------------------------------------------

func (t *wikiTool) doSearch(ctx context.Context, call ToolCallContext, start time.Time, query, lang string, maxResults int) (ToolResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return failResult(start, "query is required for search"), nil
	}

	results, err := wikiSearchAPI(ctx, call, query, lang, maxResults)
	if err != nil {
		return failResult(start, "search failed: "+err.Error()), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("query: %s\nlang: %s\nresults: %d\n", query, lang, len(results)))
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, r))
	}

	output := strings.Join(lines, "\n")
	return ToolResult{OK: true, Output: output, Stdout: output, DurationMS: time.Since(start).Milliseconds()}, nil
}

// ---------------------------------------------------------------------------
// Cache layer
// ---------------------------------------------------------------------------

type wikiArticle struct {
	Title     string    `json:"title"`
	PageID    int       `json:"page_id"`
	Extract   string    `json:"extract"`
	Lang      string    `json:"lang"`
	FetchedAt time.Time `json:"fetched_at"`
}

func wikiCacheDir(call ToolCallContext, lang string) string {
	base := call.HostWorkspaceDir
	if base == "" {
		base = call.WorkspaceDir
	}
	return filepath.Join(base, ".v100", "wiki", lang)
}

func wikiSlug(title string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "_")
	// Keep only filename-safe characters
	reg := regexp.MustCompile(`[^a-z0-9_áàâäéèêëíìîïóòôöúùûüçñ]`)
	s = reg.ReplaceAllString(s, "")
	s = regexp.MustCompile(`_+`).ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func wikiReadCache(cacheDir, slug string) (*wikiArticle, error) {
	path := filepath.Join(cacheDir, slug+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a wikiArticle
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func wikiWriteCache(cacheDir, slug string, article *wikiArticle) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(article, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(cacheDir, slug+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Wikipedia API client
// ---------------------------------------------------------------------------

func wikiAPIBase(lang string) string {
	return fmt.Sprintf("https://%s.wikipedia.org/w/api.php", lang)
}

func wikiFetchArticle(ctx context.Context, call ToolCallContext, title, lang string, sentences int) (*wikiArticle, error) {
	apiURL := wikiAPIBase(lang)

	exIntro := ""
	if sentences > 0 {
		exIntro = "&exintro=true"
	}

	url := fmt.Sprintf("%s?action=query&prop=extracts&explaintext=true&format=json&titles=%s%s&redirects=true",
		apiURL, wikiURLEncode(title), exIntro)

	body, err := wikiHTTPGet(ctx, call, url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	return wikiParseArticleResponse(body, lang)
}

func wikiSearchAPI(ctx context.Context, call ToolCallContext, query, lang string, maxResults int) ([]string, error) {
	apiURL := wikiAPIBase(lang)
	url := fmt.Sprintf("%s?action=query&list=search&srsearch=%s&srlimit=%d&format=json",
		apiURL, wikiURLEncode(query), maxResults)

	body, err := wikiHTTPGet(ctx, call, url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	return wikiParseSearchResponse(body)
}

func wikiHTTPGet(ctx context.Context, call ToolCallContext, url string) ([]byte, error) {
	// If we have a docker session, use curl inside the session
	if call.Session != nil && call.Session.Type() == "docker" {
		return wikiHTTPGetInSession(ctx, call, url)
	}

	timeout := 20 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "v100/1.0 (+https://github.com/tripledoublev/v100)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Fall back to session if available
		if call.Session != nil {
			return wikiHTTPGetInSession(ctx, call, url)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
}

func wikiHTTPGetInSession(ctx context.Context, call ToolCallContext, url string) ([]byte, error) {
	timeout := 20 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	script := fmt.Sprintf("curl -sS -L --max-time 10 -A 'v100/1.0' '%s'", url)
	res, err := call.Session.Run(ctx, executor.RunRequest{
		Command: "sh",
		Args:    []string{"-lc", script},
		Dir:     ".",
		Env:     []string{},
	})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("curl exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return []byte(res.Stdout), nil
}

// ---------------------------------------------------------------------------
// Wikipedia API response parsing
// ---------------------------------------------------------------------------

func wikiParseArticleResponse(data []byte, lang string) (*wikiArticle, error) {
	var resp struct {
		Query struct {
			Pages map[string]struct {
				PageID  int    `json:"pageid"`
				Title   string `json:"title"`
				Extract string `json:"extract"`
				Missing bool   `json:"missing,omitempty"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}

	for _, page := range resp.Query.Pages {
		if page.Missing || page.Extract == "" {
			continue
		}
		return &wikiArticle{
			Title:     page.Title,
			PageID:    page.PageID,
			Extract:   page.Extract,
			Lang:      lang,
			FetchedAt: time.Now().UTC(),
		}, nil
	}

	return nil, nil // not found
}

func wikiParseSearchResponse(data []byte) ([]string, error) {
	var resp struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}

	var results []string
	for _, s := range resp.Query.Search {
		snippet := wikiStripHTML(s.Snippet)
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		results = append(results, fmt.Sprintf("%s — %s", s.Title, snippet))
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func wikiURLEncode(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func wikiStripHTML(s string) string {
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.TrimSpace(s)
}

// wikiTruncateSentences keeps approximately n sentences from the text.
func wikiTruncateSentences(text string, n int) string {
	if n <= 0 || text == "" {
		return text
	}

	count := 0
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			count++
			if count >= n {
				end := i + 1
				for end < len(text) {
					ch, size := utf8.DecodeRuneInString(text[end:])
					if ch != ' ' && ch != '\n' && ch != '\r' && ch != '\t' {
						break
					}
					end += size
				}
				return text[:end]
			}
		}
	}
	return text
}

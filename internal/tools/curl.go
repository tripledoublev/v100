package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type curlFetchTool struct{}

func CurlFetch() Tool { return &curlFetchTool{} }

func (t *curlFetchTool) Name() string { return "curl_fetch" }
func (t *curlFetchTool) Description() string {
	return "Fetch a URL over HTTP(S) and return readable text."
}
func (t *curlFetchTool) DangerLevel() DangerLevel { return Safe }
func (t *curlFetchTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *curlFetchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["url"],
		"properties": {
			"url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
			"max_bytes": {"type": "integer", "description": "Maximum bytes to read from response body.", "default": 131072}
		}
	}`)
}

func (t *curlFetchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string"},
			"status": {"type": "integer"},
			"content_type": {"type": "string"},
			"text": {"type": "string"}
		}
	}`)
}

func (t *curlFetchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
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

	timeout := 20 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return failResult(start, "request build failed: "+err.Error()), nil
	}
	req.Header.Set("User-Agent", "v100/1.0 (+https://github.com/tripledoublev/v100)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failResult(start, "request failed: "+err.Error()), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, a.MaxBytes))
	if err != nil {
		return failResult(start, "read failed: "+err.Error()), nil
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	text := string(body)
	if strings.Contains(contentType, "text/html") || looksLikeHTML(text) {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(empty response body)"
	}

	output := fmt.Sprintf("url: %s\nstatus: %d\ncontent_type: %s\n\n%s", url, resp.StatusCode, contentType, text)
	return ToolResult{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 400,
		Output:     output,
		Stdout:     output,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t]+`)
	reNL     = regexp.MustCompile(`\n{3,}`)
)

func looksLikeHTML(s string) bool {
	ls := strings.ToLower(s)
	return strings.Contains(ls, "<html") || strings.Contains(ls, "<body") || strings.Contains(ls, "<p>")
}

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = strings.ReplaceAll(s, "</div>", "\n")
	s = strings.ReplaceAll(s, "</li>", "\n")
	s = strings.ReplaceAll(s, "</h1>", "\n")
	s = strings.ReplaceAll(s, "</h2>", "\n")
	s = strings.ReplaceAll(s, "</h3>", "\n")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = reSpace.ReplaceAllString(s, " ")
	s = reNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

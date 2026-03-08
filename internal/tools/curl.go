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

	"github.com/tripledoublev/v100/internal/core/executor"
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
	if call.Session != nil && call.Session.Type() == "docker" {
		return t.execInSession(ctx, call, start, url, a.MaxBytes)
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

func (t *curlFetchTool) execInSession(ctx context.Context, call ToolCallContext, start time.Time, url string, maxBytes int64) (ToolResult, error) {
	timeout := 20 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	const bodyMarker = "__V100_CURL_BODY__"
	script := `
set -eu
headers="$(mktemp)"
body="$(mktemp)"
meta="$(mktemp)"
trap 'rm -f "$headers" "$body" "$meta"' EXIT
curl -sS -L --max-time "$V100_TIMEOUT_SECONDS" -A "$V100_UA" -D "$headers" -o "$body" -w '%{http_code}\n%{content_type}\n' "$V100_URL" >"$meta"
cat "$meta"
printf '\n` + bodyMarker + `\n'
head -c "$V100_MAX_BYTES" "$body"
`

	res, err := call.Session.Run(ctx, executor.RunRequest{
		Command: "sh",
		Args:    []string{"-lc", script},
		Dir:     ".",
		Env: []string{
			"V100_URL=" + url,
			fmt.Sprintf("V100_MAX_BYTES=%d", maxBytes),
			fmt.Sprintf("V100_TIMEOUT_SECONDS=%d", int(timeout/time.Second)),
			"V100_UA=v100/1.0 (+https://github.com/tripledoublev/v100)",
		},
	})
	if err != nil {
		return failResult(start, "request failed: "+err.Error()), nil
	}
	if res.ExitCode != 0 {
		out := strings.TrimSpace(res.Stderr)
		if out == "" {
			out = strings.TrimSpace(res.Stdout)
		}
		return failResult(start, "request failed: "+out), nil
	}

	status, contentType, text, err := parseCurlSessionOutput(res.Stdout, bodyMarker)
	if err != nil {
		return failResult(start, "parse failed: "+err.Error()), nil
	}
	if strings.Contains(contentType, "text/html") || looksLikeHTML(text) {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(empty response body)"
	}

	output := fmt.Sprintf("url: %s\nstatus: %d\ncontent_type: %s\n\n%s", url, status, contentType, text)
	return ToolResult{
		OK:         status >= 200 && status < 400,
		Output:     output,
		Stdout:     output,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func parseCurlSessionOutput(stdout, bodyMarker string) (int, string, string, error) {
	idx := strings.Index(stdout, "\n"+bodyMarker+"\n")
	if idx == -1 {
		return 0, "", "", fmt.Errorf("missing body marker")
	}
	meta := strings.TrimSpace(stdout[:idx])
	body := stdout[idx+len("\n"+bodyMarker+"\n"):]
	lines := strings.Split(meta, "\n")
	if len(lines) < 2 {
		return 0, "", "", fmt.Errorf("missing curl metadata")
	}
	statusLine := strings.TrimSpace(lines[len(lines)-2])
	contentType := strings.TrimSpace(lines[len(lines)-1])
	var status int
	if _, err := fmt.Sscanf(statusLine, "%d", &status); err != nil {
		return 0, "", "", fmt.Errorf("invalid status %q", statusLine)
	}
	return status, strings.ToLower(contentType), body, nil
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

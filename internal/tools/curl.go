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
	"unicode/utf8"

	"github.com/tripledoublev/v100/internal/core/executor"
)

type curlFetchTool struct{}

type fetchedHTTPResponse struct {
	status      int
	contentType string
	body        []byte
}

type fetchedHTTPBody struct {
	status      int
	contentType string
	text        string
}

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

func fetchHTTPBody(ctx context.Context, call ToolCallContext, start time.Time, url string, maxBytes int64) (fetchedHTTPBody, *ToolResult, error) {
	resp, fail, err := fetchHTTP(ctx, call, start, url, maxBytes)
	if err != nil {
		return fetchedHTTPBody{}, nil, err
	}
	if fail != nil {
		return fetchedHTTPBody{}, fail, nil
	}
	return fetchedHTTPBody{
		status:      resp.status,
		contentType: resp.contentType,
		text:        describeHTTPBody(resp.contentType, resp.body),
	}, nil, nil
}

func fetchHTTP(ctx context.Context, call ToolCallContext, start time.Time, url string, maxBytes int64) (fetchedHTTPResponse, *ToolResult, error) {
	if call.Session != nil && call.Session.Type() == "docker" {
		return fetchHTTPInSession(ctx, call, start, url, maxBytes)
	}

	timeout := 20 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		res := failResult(start, "request build failed: "+err.Error())
		return fetchedHTTPResponse{}, &res, nil
	}
	req.Header.Set("User-Agent", "v100/1.0 (+https://github.com/tripledoublev/v100)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// If direct HTTP fails and a session is available, try via the session.
		// This handles environments where the Go HTTP client is blocked for specific
		// hosts but shell curl can reach them (e.g. cbc.ca in some sandboxes).
		if call.Session != nil {
			return fetchHTTPInSession(ctx, call, start, url, maxBytes)
		}
		fail := failResult(start, "request failed: "+err.Error())
		return fetchedHTTPResponse{}, &fail, nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		res := failResult(start, "read body: "+err.Error())
		return fetchedHTTPResponse{}, &res, nil
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	return fetchedHTTPResponse{
		status:      resp.StatusCode,
		contentType: contentType,
		body:        body,
	}, nil, nil
}

func fetchHTTPInSession(ctx context.Context, call ToolCallContext, start time.Time, url string, maxBytes int64) (fetchedHTTPResponse, *ToolResult, error) {
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
		res := failResult(start, "request failed: "+err.Error())
		return fetchedHTTPResponse{}, &res, nil
	}
	if res.ExitCode != 0 {
		out := strings.TrimSpace(res.Stderr)
		if out == "" {
			out = strings.TrimSpace(res.Stdout)
		}
		fail := failResult(start, "request failed: "+out)
		return fetchedHTTPResponse{}, &fail, nil
	}

	status, contentType, text, err := parseCurlSessionOutput(res.Stdout, bodyMarker)
	if err != nil {
		fail := failResult(start, "parse failed: "+err.Error())
		return fetchedHTTPResponse{}, &fail, nil
	}
	return fetchedHTTPResponse{
		status:      status,
		contentType: contentType,
		body:        []byte(text),
	}, nil, nil
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

func describeHTTPBody(contentType string, body []byte) string {
	if len(body) == 0 {
		return "(empty response body)"
	}
	if !isReadableHTTPContent(contentType, body) {
		return fmt.Sprintf("[non-text response omitted: %s, %d bytes]", displayHTTPContentType(contentType, body), len(body))
	}
	text := string(body)
	if strings.Contains(contentType, "text/html") || looksLikeHTML(text) {
		text = extractReadableHTML(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "(empty response body)"
	}
	return text
}

func isReadableHTTPContent(contentType string, body []byte) bool {
	ct := displayHTTPContentType(contentType, body)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	if strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.Contains(ct, "yaml") || strings.Contains(ct, "javascript") {
		return true
	}
	if strings.Contains(ct, "x-www-form-urlencoded") || strings.Contains(ct, "svg+xml") {
		return true
	}
	if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") {
		return false
	}
	if strings.Contains(ct, "pdf") || strings.Contains(ct, "octet-stream") || strings.Contains(ct, "zip") {
		return false
	}
	if !utf8.Valid(body) {
		return false
	}
	control := 0
	for _, b := range body {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 {
			control++
		}
	}
	return control*20 <= len(body)
}

func displayHTTPContentType(contentType string, body []byte) string {
	ct := strings.TrimSpace(strings.ToLower(contentType))
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	if ct != "" {
		return ct
	}
	return strings.ToLower(http.DetectContentType(body))
}

var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t]+`)
	reNL     = regexp.MustCompile(`\n{3,}`)
	reTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reH1     = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reH2     = regexp.MustCompile(`(?is)<h2[^>]*>(.*?)</h2>`)
	reP      = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	reLI     = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
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

func extractReadableHTML(s string) string {
	title := firstHTMLTextMatch(reTitle, s)
	headings := uniqueHTMLMatches(reH1, s, 3)
	if len(headings) < 3 {
		headings = append(headings, uniqueHTMLMatches(reH2, s, 3-len(headings))...)
		headings = dedupeStrings(headings, 3)
	}

	paragraphs := uniqueHTMLMatches(reP, s, 4)
	paragraphs = filterSignalLines(paragraphs, 40, 320)
	if len(paragraphs) == 0 {
		items := uniqueHTMLMatches(reLI, s, 6)
		paragraphs = filterSignalLines(items, 30, 220)
	}

	var out []string
	if title != "" {
		out = append(out, "title: "+title)
	}
	for _, h := range headings {
		out = append(out, "heading: "+h)
	}
	for _, p := range paragraphs {
		out = append(out, "snippet: "+p)
	}

	if len(out) == 0 {
		return htmlToText(s)
	}
	return strings.Join(out, "\n")
}

func firstHTMLTextMatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return cleanHTMLFragment(m[1])
}

func uniqueHTMLMatches(re *regexp.Regexp, s string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	matches := re.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, min(limit, len(matches)))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		text := cleanHTMLFragment(m[1])
		if text == "" {
			continue
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func cleanHTMLFragment(s string) string {
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = reSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	return strings.Trim(s, "-| ")
}

func filterSignalLines(lines []string, minLen, maxLen int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = collapseLineNoise(line)
		if len(line) < minLen || len(line) > maxLen {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "javascript") || strings.Contains(lower, "cookie") || strings.Contains(lower, "privacy") {
			continue
		}
		if strings.Count(line, "{")+strings.Count(line, "}") > 2 {
			continue
		}
		out = append(out, line)
	}
	return dedupeStrings(out, 4)
}

func collapseLineNoise(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 0 {
		s = strings.TrimSpace(s)
	}
	return s
}

func dedupeStrings(lines []string, limit int) []string {
	seen := make(map[string]struct{}, len(lines))
	out := make([]string, 0, min(limit, len(lines)))
	for _, line := range lines {
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, line)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

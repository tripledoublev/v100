package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core/executor"
)

type fakeDockerSession struct {
	lastReq executor.RunRequest
	result  executor.Result
}

func (s *fakeDockerSession) ID() string                  { return "run-1" }
func (s *fakeDockerSession) Type() string                { return "docker" }
func (s *fakeDockerSession) Start(context.Context) error { return nil }
func (s *fakeDockerSession) Close() error                { return nil }
func (s *fakeDockerSession) Workspace() string           { return "/tmp/workspace" }
func (s *fakeDockerSession) Run(_ context.Context, req executor.RunRequest) (executor.Result, error) {
	s.lastReq = req
	return s.result, nil
}

func TestCurlFetchUsesDockerSession(t *testing.T) {
	session := &fakeDockerSession{
		result: executor.Result{
			ExitCode: 0,
			Stdout:   "200\ntext/plain\n\n__V100_CURL_BODY__\nhello from sandbox\n",
		},
	}

	call := ToolCallContext{Session: session}
	args, err := json.Marshal(map[string]any{
		"url":       "https://example.com",
		"max_bytes": 64,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := CurlFetch().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("curl_fetch failed: %s", res.Output)
	}
	if session.lastReq.Command != "sh" {
		t.Fatalf("command = %q, want sh", session.lastReq.Command)
	}
	if len(session.lastReq.Args) < 2 || session.lastReq.Args[0] != "-lc" {
		t.Fatalf("args = %v, want sh -lc script", session.lastReq.Args)
	}
	if got := envValue(session.lastReq.Env, "V100_URL"); got != "https://example.com" {
		t.Fatalf("V100_URL = %q, want https://example.com", got)
	}
	if got := envValue(session.lastReq.Env, "V100_MAX_BYTES"); got != "64" {
		t.Fatalf("V100_MAX_BYTES = %q, want 64", got)
	}
	if res.Output == "" || res.Stdout == "" {
		t.Fatalf("expected output payload, got %+v", res)
	}
}

func TestParseCurlSessionOutput(t *testing.T) {
	status, contentType, body, err := parseCurlSessionOutput("204\ntext/html\n\n__V100_CURL_BODY__\n<body>ok</body>", "__V100_CURL_BODY__")
	if err != nil {
		t.Fatal(err)
	}
	if status != 204 {
		t.Fatalf("status = %d, want 204", status)
	}
	if contentType != "text/html" {
		t.Fatalf("contentType = %q, want text/html", contentType)
	}
	if body != "<body>ok</body>" {
		t.Fatalf("body = %q, want html body", body)
	}
}

func TestCurlFetchOmitsBinaryImageBody(t *testing.T) {
	session := &fakeDockerSession{
		result: executor.Result{
			ExitCode: 0,
			Stdout:   "200\nimage/png\n\n__V100_CURL_BODY__\n\x89PNG\r\n\x1a\n\x00\x00binary",
		},
	}

	call := ToolCallContext{Session: session}
	args, err := json.Marshal(map[string]any{
		"url": "https://example.com/image.png",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := CurlFetch().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("curl_fetch failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "[non-text response omitted: image/png") {
		t.Fatalf("expected non-text summary, got %q", res.Output)
	}
	if strings.Contains(res.Output, "binary") || strings.Contains(res.Output, "PNG\r\n") {
		t.Fatalf("expected raw binary body to be omitted, got %q", res.Output)
	}
}

func TestDescribeHTTPBodyExtractsSignalFromHTML(t *testing.T) {
	body := []byte(`
		<html>
		  <head><title>Example News Story</title><script>console.log("noise")</script></head>
		  <body>
		    <h1>Transit expansion approved</h1>
		    <p>City council approved a major transit expansion after a six-hour vote on Tuesday night.</p>
		    <p>The first phase will start this summer and prioritize the busiest commuter corridors.</p>
		  </body>
		</html>
	`)

	got := describeHTTPBody("text/html; charset=utf-8", body)
	for _, want := range []string{
		"title: Example News Story",
		"heading: Transit expansion approved",
		"snippet: City council approved a major transit expansion after a six-hour vote on Tuesday night.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in extracted HTML summary, got %q", want, got)
		}
	}
	if strings.Contains(got, "console.log") {
		t.Fatalf("expected script noise to be removed, got %q", got)
	}
}

func TestDescribeHTTPBodyAvoidsReturningScriptSludgeWhenSignalExists(t *testing.T) {
	body := []byte(`
		<html>
		  <head><title>Global News | Breaking, Latest News and Video for Canada</title></head>
		  <body>
		    <script>var headlineSelector = ".c-posts__headlineText"; window.chartbeatFlicker = true;</script>
		    <h2>Top Stories</h2>
		    <li>Federal budget talks intensify ahead of deadline</li>
		    <li>Storm warning issued for coastal communities</li>
		  </body>
		</html>
	`)

	got := describeHTTPBody("text/html; charset=utf-8", body)
	if strings.Contains(got, "chartbeatFlicker") || strings.Contains(got, "headlineSelector") {
		t.Fatalf("expected JS sludge to be removed, got %q", got)
	}
	if !strings.Contains(got, "title: Global News | Breaking, Latest News and Video for Canada") {
		t.Fatalf("expected extracted title, got %q", got)
	}
	if !strings.Contains(got, "Federal budget talks intensify ahead of deadline") {
		t.Fatalf("expected extracted list item signal, got %q", got)
	}
}

func TestWebExtractUsesSharedHTTPExtraction(t *testing.T) {
	session := &fakeDockerSession{
		result: executor.Result{
			ExitCode: 0,
			Stdout:   "200\ntext/html\n\n__V100_CURL_BODY__\n<html><head><title>Signal Page</title></head><body><h1>Main heading</h1><p>This page contains a useful extracted summary for operators to read quickly.</p></body></html>",
		},
	}

	call := ToolCallContext{Session: session}
	args, err := json.Marshal(map[string]any{
		"url": "https://example.com/signal",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := WebExtract().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("web_extract failed: %s", res.Output)
	}
	for _, want := range []string{"title: Signal Page", "heading: Main heading", "snippet: This page contains a useful extracted summary for operators to read quickly."} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("expected %q in web_extract output, got %q", want, res.Output)
		}
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return item[len(prefix):]
		}
	}
	return ""
}

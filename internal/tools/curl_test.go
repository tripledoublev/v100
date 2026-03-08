package tools

import (
	"context"
	"encoding/json"
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

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return item[len(prefix):]
		}
	}
	return ""
}

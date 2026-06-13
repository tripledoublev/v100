package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestShSanitizesSandboxPaths(t *testing.T) {
	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "printf '%s' \"$PWD/file.txt\"",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}
	if res.Stdout != "/workspace/file.txt" {
		t.Fatalf("expected sanitized stdout, got %q", res.Stdout)
	}
	if !strings.Contains(res.Output, "/workspace/file.txt") {
		t.Fatalf("expected sanitized output payload, got %q", res.Output)
	}
	if strings.Contains(res.Output, sandboxDir) || strings.Contains(res.Stdout, sandboxDir) {
		t.Fatalf("host sandbox path leaked in result: output=%q stdout=%q", res.Output, res.Stdout)
	}
}

func TestShStreamsSanitizedOutputDeltas(t *testing.T) {
	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	var mu sync.Mutex
	var got []string
	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
		EmitOutputDelta: func(stream, text string) error {
			mu.Lock()
			got = append(got, stream+":"+text)
			mu.Unlock()
			return nil
		},
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "printf '%s' \"$PWD/out.txt\"; printf '%s' \"$PWD/err.txt\" >&2",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 streamed deltas, got %d (%v)", len(got), got)
	}
	hasStdout, hasStderr := false, false
	for _, d := range got {
		switch d {
		case "stdout:/workspace/out.txt":
			hasStdout = true
		case "stderr:/workspace/err.txt":
			hasStderr = true
		default:
			t.Fatalf("unexpected delta: %q", d)
		}
	}
	if !hasStdout || !hasStderr {
		t.Fatalf("missing expected deltas: stdout=%v stderr=%v got=%v", hasStdout, hasStderr, got)
	}
}

func TestShDoesNotExposeParentEnvironmentSecrets(t *testing.T) {
	t.Setenv("V100_SECRET_TEST", "hidden-value")

	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "printf '%s' \"${V100_SECRET_TEST:-}\"",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Fatalf("expected sanitized env to hide parent secret, got stdout %q", res.Stdout)
	}
}

func TestShAllowsExplicitEnvPassthroughAndRedactsOutput(t *testing.T) {
	secret := "allowed-secret-value"

	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	var deltas []string
	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
		Env:          []string{"V100_ALLOWED_TOKEN=" + secret},
		RedactText:   tools.NewSecretRedactor([]string{"*_TOKEN"}, []string{"V100_ALLOWED_TOKEN=" + secret}).RedactText,
		EmitOutputDelta: func(stream, text string) error {
			deltas = append(deltas, stream+":"+text)
			return nil
		},
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "printf '%s' \"$V100_ALLOWED_TOKEN\"",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}
	for _, got := range []string{res.Stdout, res.Output, strings.Join(deltas, "\n")} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret leaked in %q", got)
		}
	}
	if !strings.Contains(res.Stdout, "[REDACTED]") {
		t.Fatalf("expected redacted stdout, got %q", res.Stdout)
	}
}

func TestShAddsGitHubAuthDiagnostic(t *testing.T) {
	sourceDir := t.TempDir()
	fakeGH := filepath.Join(sourceDir, "gh")
	if err := os.WriteFile(fakeGH, []byte("#!/bin/sh\necho 'To get started with GitHub CLI, please run: gh auth login' >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "PATH=\"$PWD:$PATH\" gh issue create",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected fake gh to fail, got %s", res.Output)
	}
	if !strings.Contains(res.Stderr, "GitHub CLI auth unavailable") {
		t.Fatalf("missing GitHub auth diagnostic in stderr: %q", res.Stderr)
	}
	if !strings.Contains(res.Output, "GitHub CLI auth unavailable") {
		t.Fatalf("missing GitHub auth diagnostic in output: %q", res.Output)
	}
}

func TestShSanitizedEnvironmentStillHasWorkingPath(t *testing.T) {
	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "command -v env >/dev/null && printf ok",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}
	if strings.TrimSpace(res.Stdout) != "ok" {
		t.Fatalf("expected PATH-preserved env to find core commands, got %q", res.Stdout)
	}
}

func TestShOutputIncludesExplicitLineBoundaries(t *testing.T) {
	sourceDir := t.TempDir()
	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"cmd": "printf '%s\\n' \"$PWD\"; printf 'total 3\\nfoo\\n'",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.Sh().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("sh failed: %s", res.Output)
	}

	var payload struct {
		StdoutLines []string `json:"stdout_lines"`
	}
	if err := json.Unmarshal([]byte(res.Output), &payload); err != nil {
		t.Fatalf("tool output was not valid JSON: %v\n%s", err, res.Output)
	}

	want := []string{"/workspace", "total 3", "foo"}
	if !reflect.DeepEqual(payload.StdoutLines, want) {
		t.Fatalf("stdout_lines = %v, want %v", payload.StdoutLines, want)
	}
}

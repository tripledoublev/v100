package tools_test

import (
	"context"
	"encoding/json"
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

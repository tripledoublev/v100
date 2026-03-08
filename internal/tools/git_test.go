package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestGitStatusUsesSandboxSession(t *testing.T) {
	sourceDir := t.TempDir()
	initGitRepo(t, sourceDir)

	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	if err := os.WriteFile(filepath.Join(sandboxDir, "sandbox-only.txt"), []byte("sandbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "source-only.txt"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}

	res, err := tools.GitStatus().Exec(context.Background(), call, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("git status failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "?? sandbox-only.txt") {
		t.Fatalf("expected sandbox file in git status, got %q", res.Output)
	}
	if strings.Contains(res.Output, "source-only.txt") {
		t.Fatalf("git status read from source workspace instead of sandbox: %q", res.Output)
	}
}

func TestGitCommitUsesSandboxSession(t *testing.T) {
	sourceDir := t.TempDir()
	initGitRepo(t, sourceDir)

	session := startHostSession(t, sourceDir)
	sandboxDir := session.Workspace()

	if err := os.WriteFile(filepath.Join(sandboxDir, "sandbox-only.txt"), []byte("sandbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "source-only.txt"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
		Session:      session,
		Mapper:       core.NewPathMapper(sourceDir, sandboxDir),
	}
	args, err := json.Marshal(map[string]any{
		"message": "sandbox commit",
		"add_all": true,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.GitCommit().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("git commit failed: %s", res.Output)
	}

	if got := strings.TrimSpace(runGitCmd(t, sourceDir, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("source repo commit count = %q, want 1", got)
	}
	if got := strings.TrimSpace(runGitCmd(t, sandboxDir, "rev-list", "--count", "HEAD")); got != "2" {
		t.Fatalf("sandbox repo commit count = %q, want 2", got)
	}
	if out := runGitStatus(t, sourceDir); !strings.Contains(out, "?? source-only.txt") {
		t.Fatalf("source repo unexpectedly mutated, git status = %q", out)
	}
	if out := strings.TrimSpace(runGitStatus(t, sandboxDir)); out != "" {
		t.Fatalf("sandbox repo should be clean after commit, git status = %q", out)
	}
}

func startHostSession(t *testing.T, sourceDir string) executor.Session {
	t.Helper()

	factory := executor.NewHostExecutor(t.TempDir())
	session, err := factory.NewSession("run-1", sourceDir)
	if err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})
	return session
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@example.com")
	runCmd(t, dir, "git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", "tracked.txt")
	runCmd(t, dir, "git", "commit", "-m", "initial")
}

func runGitStatus(t *testing.T, dir string) string {
	t.Helper()
	return runGitCmd(t, dir, "status", "--short")
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runCmd(t, dir, "git", args...)
}

func runCmd(t *testing.T, dir, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestHostSessionStartMaterializesWorkspace(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "nested", "child.txt"), []byte("child"), 0o644); err != nil {
		t.Fatal(err)
	}

	factory := NewHostExecutor(filepath.Join(sourceDir, "runs"))
	session, err := factory.NewSession("run-1", sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID() != "run-1" {
		t.Fatalf("ID() = %q, want run-1", session.ID())
	}
	if session.Type() != "host" {
		t.Fatalf("Type() = %q, want host", session.Type())
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	workspace := session.Workspace()
	for _, rel := range []string{"root.txt", filepath.Join("nested", "child.txt")} {
		if _, err := os.Stat(filepath.Join(workspace, rel)); err != nil {
			t.Fatalf("workspace missing %s: %v", rel, err)
		}
	}
}

func TestHostSessionRunUsesSourceWorkspaceWhenDisabled(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := &HostSession{
		runID:           "run-1",
		sourceWorkspace: sourceDir,
		sandboxDir:      filepath.Join(t.TempDir(), "workspace"),
		Enabled:         false,
	}

	res, err := session.Run(context.Background(), RunRequest{
		Command: "sh",
		Args:    []string{"-c", "test -f marker.txt && printf ok"},
		Dir:     ".",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "ok" {
		t.Fatalf("expected stdout ok, got %q", res.Stdout)
	}
	if got := session.Workspace(); got != sourceDir {
		t.Fatalf("Workspace() = %q, want %q", got, sourceDir)
	}
}

func TestHostSessionRunDoesNotInheritParentEnvironment(t *testing.T) {
	t.Setenv("V100_PARENT_ONLY_SECRET", "hidden")

	sourceDir := t.TempDir()
	session := &HostSession{
		runID:           "run-1",
		sourceWorkspace: sourceDir,
		sandboxDir:      filepath.Join(t.TempDir(), "workspace"),
		Enabled:         false,
	}

	res, err := session.Run(context.Background(), RunRequest{
		Command: "sh",
		Args:    []string{"-c", "printf '%s' \"${V100_PARENT_ONLY_SECRET:-}\""},
		Dir:     ".",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", res.ExitCode, res.Stderr)
	}
	if res.Stdout != "" {
		t.Fatalf("expected parent env secret to be absent, got %q", res.Stdout)
	}
}

func TestHostSessionRunPassesExplicitEnvironment(t *testing.T) {
	sourceDir := t.TempDir()
	session := &HostSession{
		runID:           "run-1",
		sourceWorkspace: sourceDir,
		sandboxDir:      filepath.Join(t.TempDir(), "workspace"),
		Enabled:         false,
	}

	res, err := session.Run(context.Background(), RunRequest{
		Command: "sh",
		Args:    []string{"-c", "printf '%s' \"$V100_ALLOWED\""},
		Dir:     ".",
		Env:     []string{"V100_ALLOWED=ok"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Stdout != "ok" {
		t.Fatalf("expected explicit env to pass, got %q", res.Stdout)
	}
}

func TestNewExecutorDisabledUsesHostSessionWithoutMaterializing(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	factory, err := NewExecutor(config.SandboxConfig{Enabled: false}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := factory.NewSession("run-disabled", sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if got := session.Workspace(); got != sourceDir {
		t.Fatalf("Workspace() = %q, want %q", got, sourceDir)
	}
}

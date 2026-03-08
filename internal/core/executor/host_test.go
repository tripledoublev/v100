package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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

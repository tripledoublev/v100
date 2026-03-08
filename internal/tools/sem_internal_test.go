package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureSemanticSemRejectsWrongBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path script helper is unix-specific")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "sem")
	body := "#!/bin/sh\nprintf 'GNU sem from parallel\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", oldPath) })
	if err := os.Setenv("PATH", dir); err != nil {
		t.Fatal(err)
	}

	err := ensureSemanticSem(context.Background())
	if err == nil {
		t.Fatal("expected wrong sem binary to be rejected")
	}
	if !strings.Contains(err.Error(), "different 'sem' binary") {
		t.Fatalf("unexpected error: %v", err)
	}
}

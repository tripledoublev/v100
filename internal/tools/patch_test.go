package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestPatchApplyUsesSandboxSession(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "target.txt"), []byte("old\n"), 0o644); err != nil {
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
		"diff":  "--- target.txt\n+++ target.txt\n@@ -1 +1 @@\n-old\n+new\n",
		"strip": 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.PatchApply().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("patch_apply failed: %s", res.Output)
	}

	sourceContent, err := os.ReadFile(filepath.Join(sourceDir, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceContent) != "old\n" {
		t.Fatalf("source workspace was mutated: %q", sourceContent)
	}

	sandboxContent, err := os.ReadFile(filepath.Join(sandboxDir, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sandboxContent) != "new\n" {
		t.Fatalf("sandbox workspace was not patched: %q", sandboxContent)
	}
}

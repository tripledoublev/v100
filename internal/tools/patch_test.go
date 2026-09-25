package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestPatchApplyRetriesWithWhitespaceTolerance(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "target.txt"), []byte("    value = 1\n"), 0o644); err != nil {
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
		"diff":  "--- target.txt\n+++ target.txt\n@@ -1 +1 @@\n- value = 1\n+ value = 2\n",
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
		t.Fatalf("patch_apply failed after whitespace-tolerant retry: %s", res.Output)
	}

	sandboxContent, err := os.ReadFile(filepath.Join(sandboxDir, "target.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sandboxContent) != " value = 2\n" {
		t.Fatalf("sandbox workspace was not patched after retry: %q", sandboxContent)
	}
}

func TestPatchApplyFailureIncludesSaferEditGuidance(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "target.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	call := tools.ToolCallContext{
		WorkspaceDir: sourceDir,
	}

	args, err := json.Marshal(map[string]any{
		"diff":  "--- target.txt\n+++ target.txt\n@@ -1 +1 @@\n-missing\n+new\n",
		"strip": 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tools.PatchApply().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected patch_apply to fail, got success: %s", res.Output)
	}
	for _, want := range []string{
		"whitespace-tolerant matching",
		"fs_write",
		"Avoid ad hoc sed",
	} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("patch failure output missing %q in %q", want, res.Output)
		}
	}
}

func TestPatchApplyWithFuzzLeavesNoOrigBackup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("BSD patch backup behavior differs")
	}
	sourceDir := t.TempDir()
	// The hunk header claims line 1 but the context sits at line 4, so GNU
	// patch applies it with an offset, which used to leave target.txt.orig.
	content := "x\ny\nz\na\nb\nc\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "target.txt"), []byte(content), 0o644); err != nil {
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
		"diff":  "--- a/target.txt\n+++ b/target.txt\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n",
		"strip": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tools.PatchApply().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.Contains(res.Output, "offset") {
		t.Fatalf("expected an offset apply, got ok=%v output=%q", res.OK, res.Output)
	}
	if _, err := os.Stat(filepath.Join(sandboxDir, "target.txt.orig")); !os.IsNotExist(err) {
		t.Fatalf("patch left a .orig backup (stat err=%v)", err)
	}
}

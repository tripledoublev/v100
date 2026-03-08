package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceFingerprintChangesWithWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := WorkspaceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := WorkspaceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatal("expected workspace fingerprint to change after file mutation")
	}
}

func TestFinalizeSandboxWorkspaceManualWritesArtifact(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()
	artifactDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(source, "same.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "old.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sandbox, "same.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "old.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "added.txt"), []byte("added\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
		ArtifactDir:      artifactDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Applied {
		t.Fatal("manual mode should not apply changes back")
	}
	if result.SkippedReason != "manual_review_required" {
		t.Fatalf("skipped_reason = %q, want manual_review_required", result.SkippedReason)
	}
	if len(result.Diff.Added) != 1 || result.Diff.Added[0] != "added.txt" {
		t.Fatalf("unexpected added diff: %#v", result.Diff.Added)
	}
	if len(result.Diff.Modified) != 1 || result.Diff.Modified[0] != "old.txt" {
		t.Fatalf("unexpected modified diff: %#v", result.Diff.Modified)
	}
	if result.ArtifactPath == "" {
		t.Fatal("expected artifact path to be set")
	}

	b, err := os.ReadFile(result.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	var artifact SandboxFinalizeResult
	if err := json.Unmarshal(b, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Mode != "manual" {
		t.Fatalf("artifact mode = %q, want manual", artifact.Mode)
	}
}

func TestFinalizeSandboxWorkspaceOnSuccessAppliesBack(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()
	artifactDir := t.TempDir()

	writeFile(t, filepath.Join(source, "keep.txt"), "keep\n")
	writeFile(t, filepath.Join(source, "change.txt"), "before\n")
	writeFile(t, filepath.Join(source, "remove.txt"), "remove\n")

	writeFile(t, filepath.Join(sandbox, "keep.txt"), "keep\n")
	writeFile(t, filepath.Join(sandbox, "change.txt"), "after\n")
	writeFile(t, filepath.Join(sandbox, "add.txt"), "add\n")

	baseline, err := WorkspaceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:                "on_success",
		Success:             true,
		SourceWorkspace:     source,
		SandboxWorkspace:    sandbox,
		BaselineFingerprint: baseline,
		ArtifactDir:         artifactDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Fatal("expected on_success mode to apply changes back")
	}

	assertFileContent(t, filepath.Join(source, "change.txt"), "after\n")
	assertFileContent(t, filepath.Join(source, "add.txt"), "add\n")
	if _, err := os.Stat(filepath.Join(source, "remove.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected remove.txt to be deleted, stat err=%v", err)
	}
}

func TestFinalizeSandboxWorkspaceOnSuccessDetectsSourceConflicts(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "change.txt"), "before\n")
	writeFile(t, filepath.Join(sandbox, "change.txt"), "after\n")

	baseline, err := WorkspaceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(source, "change.txt"), "source-edited\n")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:                "on_success",
		Success:             true,
		SourceWorkspace:     source,
		SandboxWorkspace:    sandbox,
		BaselineFingerprint: baseline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied {
		t.Fatal("expected conflicting source workspace to block apply-back")
	}
	if !result.Conflict {
		t.Fatal("expected conflict flag to be set")
	}
	if result.SkippedReason != "source_workspace_changed" {
		t.Fatalf("skipped_reason = %q, want source_workspace_changed", result.SkippedReason)
	}
	assertFileContent(t, filepath.Join(source, "change.txt"), "source-edited\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", path, string(b), want)
	}
}

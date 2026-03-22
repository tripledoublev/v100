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

func TestFinalizeSandboxWorkspaceSkipsCacheDirectories(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, ".cache", "go-build", "artifact"), "noise\n")
	writeFile(t, filepath.Join(sandbox, ".cache", "go-mod", "pkg", "modfile"), "noise\n")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Diff.Empty() {
		t.Fatalf("expected cache-only changes to be ignored, got diff %+v", result.Diff)
	}
}

func TestFinalizeSandboxWorkspaceSkipsGoTelemetry(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, ".config", "go", "telemetry", "local", "count"), "noise\n")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Diff.Empty() {
		t.Fatalf("expected telemetry-only changes to be ignored, got diff %+v", result.Diff)
	}
}

func TestFinalizeSandboxWorkspaceSkipsExhaustiveByproducts(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "main.go"), "package main\n")
	writeFile(t, filepath.Join(sandbox, "main.go"), "package main\n")

	// Noise to skip
	writeFile(t, filepath.Join(sandbox, "runs", "abc", "trace.jsonl"), "noise")
	writeFile(t, filepath.Join(sandbox, "exports", "run.tar.gz"), "noise")
	writeFile(t, filepath.Join(sandbox, ".gocache", "00", "abc"), "noise")
	writeFile(t, filepath.Join(sandbox, ".gomodcache", "pkg", "mod"), "noise")
	writeFile(t, filepath.Join(sandbox, ".npm", "_cacache", "index"), "noise")
	writeFile(t, filepath.Join(sandbox, "node_modules", "express", "index.js"), "noise")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Diff.Empty() {
		t.Fatalf("expected exhaustive byproducts to be ignored, got diff: %+v", result.Diff)
	}
}

func TestFinalizeSandboxWorkspaceHonorsV100Ignore(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, ".v100ignore"), "tmp/\n*.log\n")
	writeFile(t, filepath.Join(sandbox, ".v100ignore"), "tmp/\n*.log\n")
	writeFile(t, filepath.Join(source, "main.go"), "package main\n")
	writeFile(t, filepath.Join(sandbox, "main.go"), "package main\n")
	writeFile(t, filepath.Join(sandbox, "tmp", "artifact.txt"), "noise")
	writeFile(t, filepath.Join(sandbox, "debug.log"), "noise")
	writeFile(t, filepath.Join(sandbox, "keep.txt"), "signal")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diff.Added) != 1 || result.Diff.Added[0] != "keep.txt" {
		t.Fatalf("expected only keep.txt to remain after .v100ignore filtering, got %+v", result.Diff)
	}
}

func TestWorkspaceDiffIncludesPreviewsForSmallFiles(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, "small.txt"), "hello world\n")

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diff.Added) != 1 || result.Diff.Added[0] != "small.txt" {
		t.Fatalf("unexpected added: %v", result.Diff.Added)
	}
	preview, ok := result.Diff.Previews["small.txt"]
	if !ok {
		t.Fatal("expected preview for small.txt")
	}
	if preview != "hello world\n" {
		t.Fatalf("preview = %q, want %q", preview, "hello world\n")
	}
}

func TestWorkspaceDiffSkipsPreviewsForLargeFiles(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()

	writeFile(t, filepath.Join(source, "keep.txt"), "same\n")
	writeFile(t, filepath.Join(sandbox, "keep.txt"), "same\n")
	// Write a file larger than 4KB
	bigContent := make([]byte, 5000)
	for i := range bigContent {
		bigContent[i] = 'x'
	}
	writeFile(t, filepath.Join(sandbox, "big.bin"), string(bigContent))

	result, err := FinalizeSandboxWorkspace(SandboxFinalizeOptions{
		Mode:             "manual",
		Success:          true,
		SourceWorkspace:  source,
		SandboxWorkspace: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diff.Previews) != 0 {
		t.Fatalf("expected no previews for large file, got %v", result.Diff.Previews)
	}
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

func TestVerifyAppliedFilesDetectsContentMismatch(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()
	artifactDir := t.TempDir()

	// Create initial files
	writeFile(t, filepath.Join(source, "file.txt"), "original\n")
	writeFile(t, filepath.Join(sandbox, "file.txt"), "modified\n")

	baseline, err := WorkspaceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}

	// Apply changes and verify they work
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

	// Verify the file was correctly written
	assertFileContent(t, filepath.Join(source, "file.txt"), "modified\n")
}

func TestVerifyAppliedFilesMultipleFiles(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()
	artifactDir := t.TempDir()

	// Create initial files
	writeFile(t, filepath.Join(source, "a.txt"), "a\n")
	writeFile(t, filepath.Join(source, "b.txt"), "b\n")
	writeFile(t, filepath.Join(source, "c.txt"), "c\n")

	// Modify some files
	writeFile(t, filepath.Join(sandbox, "a.txt"), "a-modified\n")
	writeFile(t, filepath.Join(sandbox, "b.txt"), "b\n")
	writeFile(t, filepath.Join(sandbox, "c.txt"), "c-modified\n")

	baseline, err := WorkspaceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}

	// Apply changes
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

	// Verify all files have correct content
	assertFileContent(t, filepath.Join(source, "a.txt"), "a-modified\n")
	assertFileContent(t, filepath.Join(source, "b.txt"), "b\n")
	assertFileContent(t, filepath.Join(source, "c.txt"), "c-modified\n")
}

func TestVerifyAppliedFilesWithLargeBinaryContent(t *testing.T) {
	source := t.TempDir()
	sandbox := t.TempDir()
	artifactDir := t.TempDir()

	// Create initial file with some content
	writeFile(t, filepath.Join(source, "data.bin"), string(make([]byte, 1000)))

	// Write binary content to sandbox (simulating a tool that writes binary files)
	binaryContent := make([]byte, 2000)
	for i := range binaryContent {
		binaryContent[i] = byte(i % 256)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(sandbox, "data.bin")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "data.bin"), binaryContent, 0o644); err != nil {
		t.Fatal(err)
	}

	baseline, err := WorkspaceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}

	// Apply changes
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

	// Verify binary content was preserved exactly
	readBack, err := os.ReadFile(filepath.Join(source, "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(readBack) != len(binaryContent) {
		t.Fatalf("file size mismatch: got %d, want %d", len(readBack), len(binaryContent))
	}
	for i, b := range readBack {
		if b != binaryContent[i] {
			t.Fatalf("binary content mismatch at byte %d: got %d, want %d", i, b, binaryContent[i])
		}
	}
}

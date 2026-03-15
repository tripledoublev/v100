package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoBuildBinaryPrefersRepoLocalBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "v100"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "go.mod"),
		filepath.Join(root, "cmd", "v100", "main.go"),
		filepath.Join(root, "v100"),
	} {
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got, ok := repoBuildBinary(root)
	if !ok {
		t.Fatal("expected repo build binary to be discovered")
	}
	want := filepath.Join(root, "v100")
	if got != want {
		t.Fatalf("repoBuildBinary() = %q, want %q", got, want)
	}
}

func TestRepoBuildBinaryRejectsNonRepoDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "v100"), []byte("x"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	if got, ok := repoBuildBinary(root); ok {
		t.Fatalf("expected no repo build binary, got %q", got)
	}
}

package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPathMapperSanitizeText(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	sandboxDir := filepath.Join(t.TempDir(), "workspace")
	mapper := NewPathMapper(sourceDir, sandboxDir)

	text := strings.Join([]string{
		"cwd=" + filepath.Join(sandboxDir, "subdir", "file.txt"),
		"src=" + filepath.Join(sourceDir, "MEMORY.md"),
		"keep=" + sandboxDir + "-other",
	}, " ")

	got := mapper.SanitizeText(text)
	if !strings.Contains(got, "cwd=/workspace/subdir/file.txt") {
		t.Fatalf("expected sandbox path to be sanitized, got %q", got)
	}
	if !strings.Contains(got, "src=/workspace/MEMORY.md") {
		t.Fatalf("expected source path to be sanitized, got %q", got)
	}
	if !strings.Contains(got, "keep="+sandboxDir+"-other") {
		t.Fatalf("expected non-boundary path to remain unchanged, got %q", got)
	}
}

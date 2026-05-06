package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageRendererDetectsIcatWhenKittyHelperExists(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "kitty")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("KITTY_WINDOW_ID", "53")
	t.Setenv("TERM", "xterm-kitty")

	r := NewImageRenderer()
	if got := r.Backend(); got != BackendIcat {
		t.Fatalf("Backend() = %v, want icat", got)
	}
}

func TestImageRendererFallsBackToTextWhenKittyHelperMissing(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("KITTY_WINDOW_ID", "53")
	t.Setenv("TERM", "xterm-kitty")

	r := NewImageRenderer()
	if got := r.Backend(); got != BackendTextOnly {
		t.Fatalf("Backend() = %v, want text-only", got)
	}
}

func TestImageRendererRenderTextFallbackIncludesImageSummary(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("KITTY_WINDOW_ID", "53")
	t.Setenv("TERM", "xterm-kitty")

	r := NewImageRenderer()
	out := r.Render([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}, 80, 0)
	if !strings.Contains(out, "PNG") {
		t.Fatalf("Render() = %q, want PNG summary", out)
	}
}

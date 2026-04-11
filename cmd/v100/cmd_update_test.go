package main

import (
	"strings"
	"testing"
)

func TestUpdateAvailableNotice(t *testing.T) {
	got := updateAvailableNotice("v0.2.14")
	if !strings.Contains(got, "Exit and run 'v100 update' in your shell") {
		t.Fatalf("notice = %q, want explicit exit-and-shell guidance", got)
	}
	if !strings.Contains(got, "v0.2.14") {
		t.Fatalf("notice = %q, want version tag", got)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintInterruptWarningStartsOnFreshLine(t *testing.T) {
	var buf bytes.Buffer
	printInterruptWarning(&buf, "interrupted by signal")

	got := buf.String()
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("interrupt warning should start on a fresh line, got %q", got)
	}
	if !strings.Contains(got, "interrupted by signal") {
		t.Fatalf("interrupt warning missing message in %q", got)
	}
}

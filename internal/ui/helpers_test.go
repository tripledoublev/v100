package ui

import (
	"strings"
	"testing"
)

func TestSmartSummaryFSListJSON(t *testing.T) {
	out := SmartSummary("fs_list", `{"entries":["a","b","c"]}`, false)
	if out != "3 items: a, b, c" {
		t.Fatalf("unexpected fs_list summary: %q", out)
	}
}

func TestSmartSummaryCompactsLongMultilineOutput(t *testing.T) {
	input := "first useful line\n\nsecond line with more detail\nthird line"
	out := SmartSummary("sh", input, false)
	if !strings.Contains(out, "3 lines") {
		t.Fatalf("expected line count in summary, got %q", out)
	}
	if !strings.Contains(out, "first useful line") {
		t.Fatalf("expected first line in summary, got %q", out)
	}
	if strings.Contains(out, "second line with more detail") {
		t.Fatalf("expected multiline detail to be collapsed, got %q", out)
	}
}

func TestSmartSummaryVerbosePreservesExpandedOutput(t *testing.T) {
	input := "alpha\nbeta"
	out := SmartSummary("sh", input, true)
	if !strings.Contains(out, " ↵ ") {
		t.Fatalf("expected verbose output to preserve expanded content, got %q", out)
	}
}

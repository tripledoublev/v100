package ui

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestAppendTraceLine_Collapse(t *testing.T) {
	m := newTestModel()

	// First line should appear as-is.
	m.appendTraceLine("line-a", core.EventToolCall)
	got := m.traceBuf.String()
	want := "line-a\n"
	if got != want {
		t.Fatalf("first line: got %q, want %q", got, want)
	}

	// Same line → collapse, show ×2.
	m.appendTraceLine("line-a", core.EventToolCall)
	got = m.traceBuf.String()
	if got == want {
		t.Fatalf("should have collapsed, but got same output")
	}
	if !strings.Contains(got, "×2") {
		t.Fatalf("collapsed line should contain ×2, got %q", got)
	}
	if cnt := strings.Count(got, "\n"); cnt != 1 {
		t.Fatalf("expected 1 line after collapse, got %d newlines: %q", cnt, got)
	}

	// Third identical line → ×3.
	m.appendTraceLine("line-a", core.EventToolCall)
	got = m.traceBuf.String()
	if !strings.Contains(got, "×3") {
		t.Fatalf("collapsed line should contain ×3, got %q", got)
	}

	// Different line resets collapse.
	m.appendTraceLine("line-b", core.EventToolCall)
	got = m.traceBuf.String()
	if cnt := strings.Count(got, "\n"); cnt != 2 {
		t.Fatalf("expected 2 lines after new event, got %d", cnt)
	}
}

func TestAppendTraceLine_CollapseDeltas(t *testing.T) {
	m := newTestModel()

	// tool.call_delta events should collapse even if the rendered text differs.
	m.appendTraceLine("delta-abc", core.EventToolOutputDelta)
	m.appendTraceLine("delta-xyz", core.EventToolOutputDelta)
	m.appendTraceLine("delta-123", core.EventToolOutputDelta)

	got := m.traceBuf.String()
	if cnt := strings.Count(got, "\n"); cnt != 1 {
		t.Fatalf("expected 1 collapsed line for 3 deltas, got %d lines: %q", cnt, got)
	}
	if !strings.Contains(got, "×3") {
		t.Fatalf("should show ×3 for 3 deltas, got %q", got)
	}

	// Non-delta event breaks the delta chain.
	m.appendTraceLine("other", core.EventToolResult)
	got = m.traceBuf.String()
	if cnt := strings.Count(got, "\n"); cnt != 2 {
		t.Fatalf("expected 2 lines after non-delta, got %d", cnt)
	}
}

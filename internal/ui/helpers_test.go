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

func TestJsonSummaryPriorityField(t *testing.T) {
	// "message" is a priority key — should be surfaced over alphabetically-first keys
	out := SmartSummary("atproto_post", `{"cid":"abc123","uri":"at://did:plc:x/app.bsky.feed.post/abc","message":"post created"}`, false)
	if !strings.Contains(out, "message") || !strings.Contains(out, "post created") {
		t.Fatalf("expected message field in summary, got %q", out)
	}
}

func TestJsonSummaryArray(t *testing.T) {
	out := SmartSummary("atproto_feed", `[{"uri":"at://a","cid":"b"},{"uri":"at://c","cid":"d"}]`, false)
	if !strings.Contains(out, "2 items") {
		t.Fatalf("expected item count in summary, got %q", out)
	}
}

func TestJsonSummaryArraySingleItem(t *testing.T) {
	out := SmartSummary("search", `[{"name":"result"}]`, false)
	if !strings.Contains(out, "1 item") {
		t.Fatalf("expected singular item count, got %q", out)
	}
}

func TestJsonSummaryEmptyObject(t *testing.T) {
	out := SmartSummary("tool", `{}`, false)
	// should not panic; empty output is fine
	_ = out
}

func TestJsonSummaryFallbackScalar(t *testing.T) {
	// No priority keys — should fall back to first alphabetical scalar
	out := SmartSummary("tool", `{"zebra":"last","alpha":"first"}`, false)
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "first") {
		t.Fatalf("expected alphabetical fallback, got %q", out)
	}
}

func TestJsonSummaryErrorFieldPriority(t *testing.T) {
	out := SmartSummary("tool", `{"code":404,"error":"not found","other":"ignored"}`, false)
	if !strings.Contains(out, "error") || !strings.Contains(out, "not found") {
		t.Fatalf("expected error field to win, got %q", out)
	}
}

func TestSmartSummaryNonJSON(t *testing.T) {
	// Plain text starting with { that is not valid JSON should fall through gracefully
	out := SmartSummary("sh", "{ not valid json }", false)
	if out == "" {
		t.Fatalf("expected non-empty output for plain text, got empty")
	}
}

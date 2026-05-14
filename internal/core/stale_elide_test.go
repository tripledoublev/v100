package core

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestElideStaleToolResults_EmptyMessages(t *testing.T) {
	msgs := []providers.Message{}
	elided := ElideStaleToolResults(msgs, 10)
	if elided != 0 {
		t.Errorf("expected 0 elided, got %d", elided)
	}
}

func TestElideStaleToolResults_ProtectWindowZero(t *testing.T) {
	msgs := []providers.Message{
		{Role: "tool", Content: strings.Repeat("x", 200), Name: "test", ToolCallID: "call-1"},
	}
	elided := ElideStaleToolResults(msgs, 0)
	if elided != 0 {
		t.Errorf("expected 0 elided with window=0, got %d", elided)
	}
	if !strings.Contains(msgs[0].Content, "x") {
		t.Errorf("message should not be modified when window=0")
	}
}

func TestElideStaleToolResults_AllMessagesProtected(t *testing.T) {
	msgs := []providers.Message{
		{Role: "tool", Content: strings.Repeat("x", 200), Name: "test", ToolCallID: "call-1"},
		{Role: "tool", Content: strings.Repeat("y", 200), Name: "test", ToolCallID: "call-2"},
		{Role: "tool", Content: strings.Repeat("z", 200), Name: "test", ToolCallID: "call-3"},
	}
	elided := ElideStaleToolResults(msgs, 10) // window > number of messages
	if elided != 0 {
		t.Errorf("expected 0 elided when window > message count, got %d", elided)
	}
}

func TestElideStaleToolResults_ElidesOldMessages(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "help"},
		{Role: "tool", Content: strings.Repeat("x", 200), Name: "fs_read", ToolCallID: "call-1"},
		{Role: "tool", Content: strings.Repeat("y", 200), Name: "sh", ToolCallID: "call-2"},
	}
	elided := ElideStaleToolResults(msgs, 1) // protect only last 1 message
	if elided != 1 {
		t.Errorf("expected 1 elided, got %d", elided)
	}
	if !strings.HasPrefix(msgs[2].Content, "[elided]") {
		t.Errorf("expected first tool message to be elided, got: %s", msgs[2].Content)
	}
	if !strings.Contains(msgs[2].Content, "fs_read") {
		t.Errorf("expected tool name in elided message, got: %s", msgs[2].Content)
	}
	if !strings.HasPrefix(msgs[3].Content, "y") {
		t.Errorf("expected protected message not to be elided, got: %s", msgs[3].Content)
	}
}

func TestElideStaleToolResults_SkipsShortMessages(t *testing.T) {
	msgs := []providers.Message{
		{Role: "tool", Content: "short", Name: "test", ToolCallID: "call-1"},
		{Role: "tool", Content: strings.Repeat("x", 150), Name: "test", ToolCallID: "call-2"},
		{Role: "tool", Content: strings.Repeat("y", 150), Name: "test", ToolCallID: "call-3"},
	}
	elided := ElideStaleToolResults(msgs, 1) // protect last 1 message
	if elided != 1 {
		t.Errorf("expected 1 elided (skip short, elide long), got %d", elided)
	}
	// First message (short) should not be elided
	if strings.Contains(msgs[0].Content, "[elided]") {
		t.Errorf("short message should not be elided")
	}
	// Second message should be elided (long, not protected)
	if !strings.HasPrefix(msgs[1].Content, "[elided]") {
		t.Errorf("long old message should be elided")
	}
}

func TestElideStaleToolResults_SkipsAlreadyElided(t *testing.T) {
	msgs := []providers.Message{
		{Role: "tool", Content: "[elided] tool:fs_read call:abc (200 chars) — preview", Name: "fs_read", ToolCallID: "call-1"},
		{Role: "tool", Content: strings.Repeat("x", 200), Name: "sh", ToolCallID: "call-2"},
		{Role: "tool", Content: strings.Repeat("y", 200), Name: "git", ToolCallID: "call-3"},
	}
	elided := ElideStaleToolResults(msgs, 1) // protect last 1, elide first 2
	if elided != 1 {
		t.Errorf("expected 1 elided (skipping already-elided first), got %d", elided)
	}
	// First message should remain unchanged (already elided, skipped)
	if !strings.Contains(msgs[0].Content, "[elided]") {
		t.Errorf("already-elided message should not be modified")
	}
	// Second message should be elided (not already elided)
	if !strings.HasPrefix(msgs[1].Content, "[elided]") {
		t.Errorf("unelidied message should be elided")
	}
}

func TestElideSummary_ShortContent(t *testing.T) {
	summary := elideSummary("short message", "fs_read", "call-123")
	if !strings.Contains(summary, "[elided]") {
		t.Errorf("summary should start with [elided]")
	}
	if !strings.Contains(summary, "fs_read") {
		t.Errorf("summary should contain tool name")
	}
	if !strings.Contains(summary, "call-123") {
		t.Errorf("summary should contain call ID")
	}
	if !strings.Contains(summary, "short message") {
		t.Errorf("summary should include content preview")
	}
}

func TestElideSummary_LongContent(t *testing.T) {
	longContent := strings.Repeat("x", 500)
	summary := elideSummary(longContent, "sh", "call-456")
	if !strings.Contains(summary, "[elided]") {
		t.Errorf("summary should start with [elided]")
	}
	if !strings.Contains(summary, "500") {
		t.Errorf("summary should include original length")
	}
	if !strings.Contains(summary, "...") {
		t.Errorf("summary should indicate truncation with ...")
	}
}

func TestShortCallID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "n/a"},
		{"short", "short"},
		{"1234567890abcdef", "12345678"},
		{"1234567890abcdefgh", "12345678"},
	}
	for _, tt := range tests {
		got := shortCallID(tt.input)
		if got != tt.expected {
			t.Errorf("shortCallID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLoop_StaleElideWindow_Default(t *testing.T) {
	loop := &Loop{Policy: nil}
	window := loop.staleElideWindow()
	if window != DefaultStaleElideWindow {
		t.Errorf("expected default window %d, got %d", DefaultStaleElideWindow, window)
	}
}

func TestLoop_StaleElideWindow_Configured(t *testing.T) {
	loop := &Loop{Policy: &policy.Policy{StaleToolElideSteps: 42}}
	window := loop.staleElideWindow()
	if window != 42 {
		t.Errorf("expected configured window 42, got %d", window)
	}
}

func TestLoop_StaleElideWindow_Disabled(t *testing.T) {
	loop := &Loop{Policy: &policy.Policy{StaleToolElideSteps: -1}}
	window := loop.staleElideWindow()
	if window != 0 {
		t.Errorf("expected window 0 when disabled, got %d", window)
	}
}

func TestLoop_ElideStaleInMessages(t *testing.T) {
	loop := &Loop{
		Policy: &policy.Policy{StaleToolElideSteps: 1},
		Messages: []providers.Message{
			{Role: "user", Content: "hello"},
			{Role: "tool", Content: strings.Repeat("x", 200), Name: "fs_read", ToolCallID: "call-1"},
			{Role: "assistant", Content: "world"},
		},
	}
	loop.elideStaleInMessages()
	// With protect window=1, protect last 1 message
	// Total 3 messages, cutoff=3-1=2, so only message 0 is processed
	// Message 0 is "user" (not "tool"), message 1 is "tool" but outside cutoff
	// So nothing gets elided; adjust to test with proper window
	loop.Messages = []providers.Message{
		{Role: "tool", Content: strings.Repeat("x", 200), Name: "fs_read", ToolCallID: "call-1"},
		{Role: "tool", Content: strings.Repeat("y", 200), Name: "sh", ToolCallID: "call-2"},
	}
	loop.elideStaleInMessages()
	// Now with 2 messages and window=1, cutoff=2-1=1, so message 0 is processed
	if !strings.HasPrefix(loop.Messages[0].Content, "[elided]") {
		t.Errorf("expected first tool message to be elided, got: %s", loop.Messages[0].Content[:50])
	}
}

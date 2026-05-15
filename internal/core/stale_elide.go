package core

import (
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/providers"
)

// DefaultStaleElideWindow is the default number of recent messages (from the end)
// protected from stale eliding. Tool result messages beyond this window are
// candidates for eliding.
//
// Policy.StaleToolElideSteps semantics:
//   - > 0 : custom protect window
//   - 0   : use DefaultStaleElideWindow (default behavior)
//   - -1  : disable eliding entirely
const DefaultStaleElideWindow = 20

// ElideStaleToolResults scans the message history and replaces old tool result
// messages with compact one-line summaries. This keeps the context lean by
// removing verbatim output from tool calls that are no longer directly relevant
// to the current step.
//
// Eliding is a deterministic, zero-cost operation (no LLM calls). It runs
// before each model call via previewMessagesForStep.
//
// Protected messages (within the recent window) are never elided.
// Messages whose content starts with "[elided]" are skipped to avoid
// double-eliding.
//
// This function returns the number of messages that were elided.
func ElideStaleToolResults(msgs []providers.Message, protectWindow int) int {
	if protectWindow <= 0 {
		return 0
	}
	n := len(msgs)
	if n <= protectWindow {
		return 0
	}

	elided := 0
	cutoff := n - protectWindow

	for i := 0; i < cutoff; i++ {
		m := &msgs[i]
		if m.Role != "tool" {
			continue
		}
		// Skip already-elided messages
		if strings.HasPrefix(m.Content, "[elided]") {
			continue
		}
		// Skip very short tool results — not worth eliding
		if len(m.Content) < 120 {
			continue
		}

		// Build a compact summary preserving key metadata
		m.Content = elideSummary(m.Content, m.Name, m.ToolCallID)
		elided++
	}
	return elided
}

// elideSummary produces a compact one-line replacement for a tool result.
// It preserves the tool name, call ID, and a head/tail preview of the output.
func elideSummary(content, toolName, callID string) string {
	const headLen = 60
	const tailLen = 60

	origLen := len(content)

	// Clean up content for preview — collapse whitespace
	preview := strings.Join(strings.Fields(content), " ")

	var head, tail string
	if len(preview) > headLen+tailLen+20 {
		head = preview[:headLen]
		tail = preview[len(preview)-tailLen:]
		return fmt.Sprintf("[elided] tool:%s call:%s (%d chars) — %s ... %s",
			toolName, shortCallID(callID), origLen, head, tail)
	}
	// Content is moderate — include a fuller preview
	if len(preview) > 150 {
		return fmt.Sprintf("[elided] tool:%s call:%s (%d chars) — %s ...",
			toolName, shortCallID(callID), origLen, preview[:140])
	}
	return fmt.Sprintf("[elided] tool:%s call:%s (%d chars) — %s",
		toolName, shortCallID(callID), origLen, preview)
}

// shortCallID returns the first 8 characters of a call ID, or "n/a".
func shortCallID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "n/a"
	}
	return id
}

// staleElideWindow returns the configured protect window, or the default.
func (l *Loop) staleElideWindow() int {
	if l.Policy != nil && l.Policy.StaleToolElideSteps > 0 {
		return l.Policy.StaleToolElideSteps
	}
	if l.Policy != nil && l.Policy.StaleToolElideSteps == -1 {
		return 0 // explicitly disabled
	}
	return DefaultStaleElideWindow
}

// elideStaleInMessages runs stale eliding on the loop's message slice.
// Called from previewMessagesForStep to apply eliding before each model call.
func (l *Loop) elideStaleInMessages() {
	window := l.staleElideWindow()
	if window <= 0 {
		return
	}
	ElideStaleToolResults(l.Messages, window)
}

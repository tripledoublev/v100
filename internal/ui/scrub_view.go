package ui

import (
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
)

func buildScrubListContent(events []core.Event, cursor, width int) (string, int, int) {
	if len(events) == 0 {
		empty := styleMuted.Render("no events")
		return empty, 0, 0
	}

	cursor = clampScrubCursor(cursor, len(events))
	prefixWidth := scrubListPrefixWidth(len(events))
	contentWidth := max(1, width-prefixWidth)
	blankPrefix := strings.Repeat(" ", prefixWidth)

	var lines []string
	cursorStart := 0
	cursorEnd := 0

	for idx := range events {
		block := renderTraceEventBlock(&events[idx], contentWidth)
		if idx == cursor {
			cursorStart = len(lines)
		}
		for row, line := range block {
			prefix := blankPrefix
			if row == 0 {
				prefix = scrubListPrefix(idx, len(events), idx == cursor)
			}
			rendered := prefix + line
			if idx == cursor {
				rendered = stylePrimary.Render(rendered)
			} else {
				rendered = styleMuted.Render(rendered)
			}
			lines = append(lines, rendered)
		}
		if idx == cursor {
			cursorEnd = max(cursorStart, len(lines)-1)
		}
	}

	return strings.Join(lines, "\n"), cursorStart, cursorEnd
}

func buildScrubDetailContent(ev *core.Event, width int) string {
	if ev == nil {
		return styleMuted.Render("no events")
	}

	title, details := describeTraceEvent(*ev)
	lines := wrapTraceEventText(title, width, "")
	if len(details) > 0 {
		lines = append(lines, "")
		lines = append(lines, stylePrimary.Render("summary"))
		for _, detail := range details {
			if strings.TrimSpace(detail) == "" {
				continue
			}
			lines = append(lines, wrapTraceEventText(detail, width, "  ")...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, stylePrimary.Render("payload"))
	lines = append(lines, strings.Split(renderStructuredForPane(formatEventPayloadPretty(ev), width), "\n")...)

	meta := []string{
		fmt.Sprintf("timestamp: %s", ev.TS.Local().Format("2006-01-02 15:04:05")),
		fmt.Sprintf("run_id: %s", ev.RunID),
		fmt.Sprintf("step_id: %s", zeroFallback(ev.StepID, "n/a")),
		fmt.Sprintf("event_id: %s", zeroFallback(ev.EventID, "n/a")),
	}
	lines = append(lines, "")
	lines = append(lines, stylePrimary.Render("meta"))
	for _, line := range meta {
		lines = append(lines, wrapTraceEventText(line, width, "  ")...)
	}

	return strings.Join(lines, "\n")
}

func renderScrubHeader(runID string, cursor, total int, ev *core.Event, focus scrubFocus, width int) string {
	title := fmt.Sprintf("v100 replay  %s", runID)
	info := fmt.Sprintf("event %d/%d", scrubDisplayIndex(cursor, total), total)
	if ev != nil {
		info += "  " + string(ev.Type)
	}
	info += "  focus:" + focus.String()

	header := tuiHeaderStyle.Render(title)
	if lipgloss.Width(header) < width {
		gap := width - lipgloss.Width(header) - lipgloss.Width(info)
		if gap > 2 {
			header += strings.Repeat(" ", gap) + tuiHeaderDimStyle.Render(info)
		}
	}

	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(header)
}

func renderScrubFooter(width int, focus scrubFocus) string {
	hints := "↑/↓/j/k navigate  pgup/pgdn/home/end jump  tab focus  s next step  q quit"
	if focus == scrubFocusDetail {
		hints = "↑/↓/j/k scroll detail  pgup/pgdn/home/end scroll  tab focus  s next step  q quit"
	}
	return tuiHeaderDimStyle.Width(width).Render(hints)
}

func scrubListPrefixWidth(total int) int {
	digits := len(fmt.Sprintf("%d", max(0, total-1)))
	return digits + 3
}

func scrubListPrefix(idx, total int, selected bool) string {
	digits := len(fmt.Sprintf("%d", max(0, total-1)))
	if selected {
		return fmt.Sprintf("▶ %*d ", digits, idx)
	}
	return fmt.Sprintf("  %*d ", digits, idx)
}

func scrubDisplayIndex(cursor, total int) int {
	if total == 0 {
		return 0
	}
	return clampScrubCursor(cursor, total) + 1
}

func zeroFallback(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

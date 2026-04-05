package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tripledoublev/v100/internal/core"
)

func scrubTestEvents(t *testing.T) []core.Event {
	t.Helper()
	return []core.Event{
		{
			Type: core.EventUserMsg,
			Payload: mustPayload(t, core.UserMsgPayload{
				Content: "First line of a longer message.\nSecond line keeps the first block tall.",
			}),
		},
		{
			Type: core.EventToolCall,
			Payload: mustPayload(t, core.ToolCallPayload{
				Name: "fs_read",
				Args: `{"path":"README.md","offset":120}`,
			}),
		},
		{
			Type: core.EventStepSummary,
			Payload: mustPayload(t, core.StepSummaryPayload{
				StepNumber:   1,
				ToolCalls:    1,
				ModelCalls:   1,
				DurationMS:   240,
				InputTokens:  10,
				OutputTokens: 20,
				CostUSD:      0.0012,
			}),
		},
	}
}

func TestScrubModelViewBeforeReady(t *testing.T) {
	m := NewScrubModel("run-abc", scrubTestEvents(t))
	if out := m.View(); !strings.Contains(out, "Initializing") {
		t.Fatalf("expected initializing message, got %q", out)
	}
}

func TestBuildScrubListContentTracksMultilineCursorOffset(t *testing.T) {
	events := scrubTestEvents(t)
	_, start, end := buildScrubListContent(events, 1, 36)
	if start <= 0 {
		t.Fatalf("expected cursor start below row 0, got %d", start)
	}
	if end < start {
		t.Fatalf("expected cursor end >= start, got start=%d end=%d", start, end)
	}
}

func TestScrubModelCursorNavigationUpdatesDetailPane(t *testing.T) {
	m := NewScrubModel("run-abc", scrubTestEvents(t))
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	if !strings.Contains(m.detailPane.View(), `README.md`) {
		t.Fatalf("detail pane missing selected event payload: %q", m.detailPane.View())
	}
}

func TestScrubModelKeepsCursorVisible(t *testing.T) {
	var events []core.Event
	for i := 0; i < 14; i++ {
		events = append(events, core.Event{
			Type: core.EventUserMsg,
			Payload: mustPayload(t, core.UserMsgPayload{
				Content: "line one for event\nline two keeps height",
			}),
		})
	}
	m := NewScrubModel("run-abc", events)
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 12})
	for i := 0; i < 10; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.listPane.YOffset <= 0 {
		t.Fatalf("expected list pane to auto-scroll, got y=%d", m.listPane.YOffset)
	}
	if m.cursorStart < m.listPane.YOffset || m.cursorStart >= m.listPane.YOffset+m.listPane.Height {
		t.Fatalf("cursor start should remain visible: start=%d end=%d y=%d h=%d",
			m.cursorStart, m.cursorEnd, m.listPane.YOffset, m.listPane.Height)
	}
}

func TestBuildScrubDetailContentShowsFullPrettyPayload(t *testing.T) {
	ev := core.Event{
		Type: core.EventUserMsg,
		Payload: mustPayload(t, core.UserMsgPayload{
			Content: strings.Repeat("A", 100) + "TAIL_MARKER",
		}),
	}
	out := buildScrubDetailContent(&ev, 72)
	if !strings.Contains(out, `"content":`) {
		t.Fatalf("expected pretty JSON in detail pane, got %q", out)
	}
	if !strings.Contains(out, "TAIL_MARKER") {
		t.Fatalf("detail payload should include full content tail, got %q", out)
	}
}

func TestScrubModelStepJumpFindsNextSummary(t *testing.T) {
	m := NewScrubModel("run-abc", scrubTestEvents(t))
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.cursor != 2 {
		t.Fatalf("cursor after step jump = %d, want 2", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.cursor != 2 {
		t.Fatalf("step jump at end should be no-op, got %d", m.cursor)
	}
}

func TestScrubModelDetailFocusScrollsWithoutMovingCursor(t *testing.T) {
	ev := core.Event{
		Type: core.EventUserMsg,
		Payload: mustPayload(t, core.UserMsgPayload{
			Content: strings.Join([]string{
				"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
			}, "\n"),
		}),
	}
	m := NewScrubModel("run-abc", []core.Event{ev})
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 12})
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	cursor := m.cursor
	before := m.detailPane.YOffset
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.cursor != cursor {
		t.Fatalf("cursor changed while detail was focused: got %d want %d", m.cursor, cursor)
	}
	if m.detailPane.YOffset <= before {
		t.Fatalf("detail pane should scroll when focused: before=%d after=%d", before, m.detailPane.YOffset)
	}
}

func TestScrubModelRendersOnTinyTerminal(t *testing.T) {
	m := NewScrubModel("run-abc", scrubTestEvents(t))
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 10})
	out := m.View()
	if !strings.Contains(out, "events") || !strings.Contains(out, "detail") {
		t.Fatalf("tiny terminal view should still render both panes: %q", out)
	}
}

package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
)

func updateKey(m *TUIModel, msg tea.KeyMsg) *TUIModel {
	updated, _ := m.Update(msg)
	return updated.(*TUIModel)
}

func TestViewRendersHeaderInBoundedHeight(t *testing.T) {
	m := NewTUIModel()
	m.width = 140
	m.height = 42
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("expected non-empty view")
	}
	first := stripANSI(lines[0])
	if !strings.Contains(first, "v100") {
		t.Fatalf("expected header on first line, got first line: %q", first)
	}
}

func TestViewKeepsClockVisibleInHeader(t *testing.T) {
	m := NewTUIModel()
	m.width = 100
	m.height = 30

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("expected non-empty view")
	}
	first := stripANSI(lines[0])
	if !strings.Contains(first, "v100") {
		t.Fatalf("expected header on first line, got %q", first)
	}
	matched, err := regexp.MatchString(`\b\d{2}:\d{2}:\d{2}\b`, first)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatalf("expected visible clock in header line, got %q", first)
	}
}

func TestToolResultRendersAsIndentedBlock(t *testing.T) {
	m := NewTUIModel()
	m.width = 80
	m.height = 24

	payload, err := json.Marshal(core.ToolResultPayload{
		Name:       "sh",
		OK:         true,
		DurationMS: 123,
		Output:     "first useful line with a lot of detail that should wrap cleanly in the transcript pane rather than making one awkward overstuffed row",
	})
	if err != nil {
		t.Fatal(err)
	}

	m.appendEvent(core.Event{
		TS:      time.Now(),
		Type:    core.EventToolResult,
		Payload: payload,
	})

	out := stripANSI(m.transcriptBuf.String())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line tool result block, got %q", out)
	}
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, "sh") && strings.Contains(line, "[123ms]") {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("expected tool metadata line in transcript, got %q", out)
	}
	if idx+1 >= len(lines) {
		t.Fatalf("expected wrapped tool summary after metadata line, got %q", out)
	}
	if !strings.HasPrefix(lines[idx+1], "             ") {
		t.Fatalf("expected indented wrapped tool summary, got %q", lines[idx+1])
	}
}

func TestViewResponsiveLayoutsPreservePanelsAndWidthBounds(t *testing.T) {
	widths := []int{92, 120, 160}
	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := NewTUIModel()
			m.width = width
			m.height = 30
			m.showTrace = true
			m.showStatus = true
			m.showMetrics = true
			m.lastEventAt = time.Now().Add(-3 * time.Second)
			m.runSummary = "A long status summary that should wrap instead of overflowing the pane width while the visual inspector, trace, and status panels remain visible."
			m.statusLine = "The harness is actively rendering multiple panes and this line should stay within the available width after wrapping."

			view := stripANSI(m.View())
			assertViewWithinWidth(t, view, width)
			for _, want := range []string{"trace", "status", "visual inspector"} {
				if !strings.Contains(view, want) {
					t.Fatalf("expected %q in responsive layout at width %d", want, width)
				}
			}
		})
	}
}

func TestViewResizeKeepsPanelsVisibleAndBounded(t *testing.T) {
	m := NewTUIModel()
	m.showTrace = true
	m.showStatus = true
	m.showMetrics = true
	m.lastEventAt = time.Now().Add(-2 * time.Second)
	m.runSummary = "Resize regression coverage for transcript, inspector, and status composition."
	m.statusLine = "This content should remain visible and wrapped after resizing the terminal."

	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 150, height: 36},
		{width: 96, height: 24},
	} {
		m.width = size.width
		m.height = size.height

		view := stripANSI(m.View())
		assertViewWithinWidth(t, view, size.width)
		for _, want := range []string{"trace", "status", "visual inspector"} {
			if !strings.Contains(view, want) {
				t.Fatalf("expected %q after resize to %dx%d", want, size.width, size.height)
			}
		}
	}
}

func TestViewSinglePaneOmitsRightColumnPanels(t *testing.T) {
	m := NewTUIModel()
	m.width = 100
	m.height = 26
	m.showTrace = false
	m.showStatus = true
	m.showMetrics = true
	m.transcript.SetContent("single pane transcript")
	m.traceView.SetContent("right pane trace body")
	m.runSummary = "right pane inspector"
	m.statusLine = "right pane status"

	view := stripANSI(m.View())
	assertViewWithinWidth(t, view, m.width)
	for _, unwanted := range []string{"visual inspector", "right pane trace body", "right pane inspector", "right pane status"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("did not expect %q in single-pane layout", unwanted)
		}
	}
	if !strings.Contains(view, "single pane transcript") {
		t.Fatal("expected transcript content in single-pane layout")
	}
}

func assertViewWithinWidth(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if line == "" {
			continue
		}
		if lipgloss.Width(line) > width {
			t.Fatalf("line exceeds width %d: %q", width, line)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inEsc {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEsc = false
			}
			continue
		}
		if ch == 0x1b {
			inEsc = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func TestFocusCycleLeftHalf(t *testing.T) {
	m := NewTUIModel()
	m.focus = focusTranscript

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusInput {
		t.Fatalf("expected focusInput after tab, got %v", m.focus)
	}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript after second tab, got %v", m.focus)
	}
}

func TestFocusHalfSwitchAndRightHalfCycling(t *testing.T) {
	m := NewTUIModel()
	m.showTrace = true
	m.showStatus = true
	m.focus = focusTranscript

	// Switch to right half with a robust fallback key.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if m.focus != focusTrace {
		t.Fatalf("expected focusTrace after half-switch, got %v", m.focus)
	}

	// Tab cycles within right half (trace <-> status).
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusStatus {
		t.Fatalf("expected focusStatus after tab in right half, got %v", m.focus)
	}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != focusTrace {
		t.Fatalf("expected focusTrace after shift+tab in right half, got %v", m.focus)
	}

	// Half-switch back to left.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript after switching back, got %v", m.focus)
	}
}

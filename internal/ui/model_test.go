package ui

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

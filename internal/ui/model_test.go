package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

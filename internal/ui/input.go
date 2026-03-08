package ui

import (
	"fmt"
	"os/exec"
	"strings"
)

func (m *TUIModel) handleMouseClick(x, y int) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	// Input box occupies the last 3 rows (top border + content + bottom border).
	inputStartY := m.height - 3
	if y >= inputStartY {
		m.focus = focusInput
		m.input.Focus()
		return
	}
	if !m.showTrace {
		m.tryClickCopyTarget(y)
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	// Left pane outer width = leftW(inner) + 2 borders. Right pane starts at leftW+2+1.
	total := m.width - 5
	leftW := total * m.leftPanePct / 100
	if leftW < 38 {
		leftW = 38
	}
	leftOuterEnd := leftW + 1 // 0-indexed: left border at 0, content 1..leftW, right border at leftW+1
	if x <= leftOuterEnd {
		m.tryClickCopyTarget(y)
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	// Right half — trace vs status
	if !m.showStatus {
		m.focus = focusTrace
		m.input.Blur()
		return
	}
	// Row where status pane starts: panes begin at row 1, trace outer = traceH+2, status starts after.
	remaining := m.height - 1 - 3 // header=1, input=3, no extra separators
	rightBudget := remaining - 4
	traceH := rightBudget * m.tracePanePct / 100
	if traceH < 4 {
		traceH = 4
	}
	tracePaneEndY := 1 + traceH + 2 // panes start row + trace outer height
	if y < tracePaneEndY {
		m.focus = focusTrace
	} else {
		m.focus = focusStatus
	}
	m.input.Blur()
}

// tryClickCopyTarget checks if the click row matches a copy icon and copies if so.
// Transcript content starts at terminal row 2 (header=row0, pane_top_border=row1).
func (m *TUIModel) tryClickCopyTarget(termY int) {
	const contentStartRow = 2
	if termY < contentStartRow {
		return
	}
	contentLine := (termY - contentStartRow) + m.transcript.YOffset
	for _, ct := range m.copyTargets {
		if contentLine == ct.lineNo || contentLine == ct.lineNo+1 {
			if err := copyToClipboard(ct.content); err != nil {
				m.statusLine = "copy failed: " + err.Error()
				m.statusMode = "error"
			} else {
				m.statusLine = "copied to clipboard!"
			}
			return
		}
	}
}

func copyToClipboard(text string) error {
	cmds := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
	}
	for _, args := range cmds {
		if _, err := exec.LookPath(args[0]); err == nil {
			c := exec.Command(args[0], args[1:]...)
			c.Stdin = strings.NewReader(text)
			return c.Run()
		}
	}
	return fmt.Errorf("no clipboard tool found (install wl-copy, xclip, or xsel)")
}

func sanitizeInputNoise(s string) string {
	if strings.HasPrefix(s, "]11;rgb:") || strings.HasPrefix(s, "\x1b]11;rgb:") {
		return ""
	}
	return s
}

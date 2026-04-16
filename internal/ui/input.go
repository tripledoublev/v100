package ui

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

var clipboardImageReader = readClipboardImage

var urlRegex = regexp.MustCompile(`https?://[^\s)\]'"]+`)

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
		m.tryClickURL(x, y)
		m.tryClickToggleTarget(y)
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
		m.tryClickURL(x, y)
		m.tryClickToggleTarget(y)
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

func (m *TUIModel) tryClickURL(termX, termY int) {
	const contentStartRow = 2
	if termY < contentStartRow {
		return
	}
	contentLine := (termY - contentStartRow) + m.transcript.YOffset
	lines := strings.Split(m.transcriptBuf.String(), "\n")
	if contentLine < 0 || contentLine >= len(lines) {
		return
	}
	rawLine := lines[contentLine]
	strippedLine := stripANSI(rawLine)

	localX := termX - 1 // 1 for left border
	if localX < 0 || localX >= len(strippedLine) {
		return
	}

	matches := urlRegex.FindAllStringIndex(strippedLine, -1)
	for _, match := range matches {
		if localX >= match[0] && localX < match[1] {
			url := strippedLine[match[0]:match[1]]
			_ = openURL(url)
			return
		}
	}
}

func openURL(u string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	case "darwin":
		cmd = "open"
		args = []string{u}
	default: // linux, freebsd, etc
		cmd = "xdg-open"
		args = []string{u}
	}
	return exec.Command(cmd, args...).Start()
}

func (m *TUIModel) tryClickToggleTarget(termY int) {
	const contentStartRow = 2
	if termY < contentStartRow {
		return
	}
	contentLine := (termY - contentStartRow) + m.transcript.YOffset
	for _, tt := range m.toggleTargets {
		if contentLine == tt.lineNo {
			for _, item := range m.history {
				if item.ID == tt.itemID {
					item.Expanded = !item.Expanded
					m.rebuildTranscript(false)
					return
				}
			}
		}
	}
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

// readClipboardImage attempts to read an image from the clipboard.
// Returns the image data (PNG format) or nil if no image is available.
func readClipboardImage() ([]byte, error) {
	// Try Wayland first
	if data, err := tryClipboardTool("wl-paste", "-t", "image/png"); err == nil {
		return data, nil
	}
	// Try X11 with xclip
	if data, err := tryClipboardTool("xclip", "-selection", "clipboard", "-t", "image/png", "-o"); err == nil {
		return data, nil
	}
	// Try X11 with xsel
	if data, err := tryClipboardImageTool("xsel", "--clipboard", "-o"); err == nil {
		return data, nil
	}
	// Try macOS
	if data, err := tryClipboardImageTool("pbpaste"); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("no image in clipboard")
}

// tryClipboardTool runs a command and returns its output if successful.
func tryClipboardTool(name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Check if output looks like valid image data (not empty)
	if len(out) == 0 {
		return nil, fmt.Errorf("empty clipboard")
	}
	return out, nil
}

func tryClipboardImageTool(name string, args ...string) ([]byte, error) {
	out, err := tryClipboardTool(name, args...)
	if err != nil {
		return nil, err
	}
	if !isPNGData(out) {
		return nil, fmt.Errorf("clipboard data is not PNG image data (detected %s)", http.DetectContentType(out))
	}
	return out, nil
}

func isPNGData(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	sig := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for i, b := range sig {
		if data[i] != b {
			return false
		}
	}
	return true
}

// imageCount returns a string describing attached images, e.g. "[Image #1]".
func imageCount(n int) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return "[Image #1]"
	}
	return fmt.Sprintf("[%d images]", n)
}

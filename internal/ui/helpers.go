package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tripledoublev/v100/internal/i18n"
)

const (
	maxOutputLines = 3
	maxOutputChars = 120
)

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func shortRunID(id string) string {
	if len(id) > 16 {
		return id[:8] + "…" + id[len(id)-6:]
	}
	if strings.TrimSpace(id) == "" {
		return "agent"
	}
	return id
}

func (m *TUIModel) removeActiveAgent(runID string) {
	if len(m.activeAgents) == 0 {
		return
	}
	for i := len(m.activeAgents) - 1; i >= 0; i-- {
		if m.activeAgents[i].RunID == runID {
			m.activeAgents = append(m.activeAgents[:i], m.activeAgents[i+1:]...)
			return
		}
	}
	// Fallback for malformed traces: pop the most recent frame.
	m.activeAgents = m.activeAgents[:len(m.activeAgents)-1]
}

func (m *TUIModel) subAgentStatusLine() string {
	if len(m.activeAgents) > 0 {
		a := m.activeAgents[len(m.activeAgents)-1]
		return fmt.Sprintf(i18n.T("ui_current_agent"), shortRunID(a.RunID), a.label)
	}
	if m.lastAgentNote != "" {
		return fmt.Sprintf(i18n.T("ui_last_agent"), m.lastAgentNote)
	}
	return "last: none"
}

func envSizeFallback() (int, int) {
	w, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	h, _ := strconv.Atoi(os.Getenv("LINES"))
	return w, h
}

func pickStatusLine(n int, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[n%len(lines)]
}

// downloadFrames holds classic braille-dot spinner frames.
var downloadFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinFrames holds simple slash rotation: / - \ |
var spinFrames = []string{"/", "─", "\\", "|"}

// DownloadSpinner returns a spinning indicator using braille-dot chars.
func DownloadSpinner(tick int) string {
	return downloadFrames[tick%len(downloadFrames)]
}

// SpinSlash returns a spinning slash indicator: / - \ |
func SpinSlash(tick int) string {
	return spinFrames[tick%len(spinFrames)]
}

// FormatTokens formats a token count for display: 0→"0", 500→"500", 1500→"1.5k", 24000→"24k".
func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000.0)
}

// FormatDuration formats milliseconds for display: 500→"0.5s", 3200→"3s", 65000→"1m5s".
func FormatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
	}
	sec := ms / 1000
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	min := sec / 60
	rem := sec % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", min)
	}
	return fmt.Sprintf("%dm%ds", min, rem)
}

// TruncateOutput truncates a string if it exceeds maxOutputLines or maxOutputChars
// when not in verbose mode. It also replaces newlines with " ↵ " for single-line display.
func TruncateOutput(s string, verbose bool) string {
	if verbose {
		return strings.ReplaceAll(s, "\n", " ↵ ")
	}

	lines := strings.Split(s, "\n")
	if len(lines) > maxOutputLines {
		s = strings.Join(lines[:maxOutputLines], "\n") + "\n..."
	}

	runes := []rune(s)
	if len(runes) > maxOutputChars {
		s = string(runes[:maxOutputChars]) + "..."
	}

	return strings.ReplaceAll(s, "\n", " ↵ ")
}

// SmartSummary provides a structured summary of a tool's output for clean TUI display.
func SmartSummary(toolName, output string, verbose bool) string {
	if verbose {
		return TruncateOutput(output, true)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	// Heuristic: summarize directory listings
	if toolName == "fs_list" || toolName == "fs_ls" {
		if strings.HasPrefix(output, "{") {
			var m struct {
				Entries []string `json:"entries"`
			}
			if json.Unmarshal([]byte(output), &m) == nil {
				return fmt.Sprintf("%d items: %s", len(m.Entries), strings.Join(m.Entries, ", "))
			}
		}
	}

	// JSON-aware summary: extract meaningful fields instead of showing "{".
	if strings.HasPrefix(output, "{") || strings.HasPrefix(output, "[") {
		if s := jsonSummary(output); s != "" {
			return s
		}
	}

	// Common case: collapse long multiline tool output into a single scan-friendly line.
	if strings.Count(output, "\n") >= 2 || len(output) > 160 {
		return compactToolSummary(output)
	}

	return TruncateOutput(output, false)
}

// jsonSummary extracts a scan-friendly one-liner from a JSON object or array.
// Returns "" if the input isn't valid JSON or yields nothing useful.
func jsonSummary(output string) string {
	// Array: report element count + type of first element.
	if strings.HasPrefix(output, "[") {
		var arr []json.RawMessage
		if json.Unmarshal([]byte(output), &arr) == nil && len(arr) > 0 {
			noun := "items"
			if len(arr) == 1 {
				noun = "item"
			}
			preview := jsonLeadingValue(arr[0])
			if preview != "" {
				return fmt.Sprintf("%d %s: %s", len(arr), noun, truncateRunes(preview, 72))
			}
			return fmt.Sprintf("%d %s", len(arr), noun)
		}
		return ""
	}

	// Object: pick the most informative scalar field.
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(output), &obj) != nil {
		return ""
	}

	// Priority key list — first match wins.
	priority := []string{
		"error", "message", "msg", "text", "content",
		"uri", "cid", "id", "handle", "did",
		"status", "state", "result", "output",
	}
	for _, key := range priority {
		if raw, ok := obj[key]; ok {
			if s := jsonScalar(raw); s != "" {
				// surface key=value for clarity
				return fmt.Sprintf("%s: %s", key, truncateRunes(s, 80))
			}
		}
	}

	// Fallback: first scalar field found, alphabetical.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s := jsonScalar(obj[k]); s != "" {
			return fmt.Sprintf("%s: %s", k, truncateRunes(s, 80))
		}
	}

	// No useful scalar; show key count.
	return fmt.Sprintf("{%d fields}", len(obj))
}

// jsonScalar returns the string value of a JSON string or number token, or "".
func jsonScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// number / bool / null — use raw token directly
	tok := strings.TrimSpace(string(raw))
	if tok == "null" || tok == "" {
		return ""
	}
	if tok[0] == '{' || tok[0] == '[' {
		return ""
	}
	return tok
}

// jsonLeadingValue returns a short preview of the first element in a JSON array.
func jsonLeadingValue(raw json.RawMessage) string {
	tok := strings.TrimSpace(string(raw))
	if len(tok) == 0 {
		return ""
	}
	if tok[0] == '{' {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			for _, key := range []string{"name", "title", "id", "uri", "handle", "text", "content"} {
				if v, ok := obj[key]; ok {
					if s := jsonScalar(v); s != "" {
						return s
					}
				}
			}
		}
		return fmt.Sprintf("{%d fields}", len(obj))
	}
	return jsonScalar(raw)
}

func compactToolSummary(output string) string {
	lines := nonEmptyLines(output)
	if len(lines) == 0 {
		return TruncateOutput(output, false)
	}
	head := collapseWhitespace(lines[0])
	head = truncateRunes(head, 72)
	if len(lines) == 1 {
		return fmt.Sprintf("1 line, %d chars: %s", len(output), head)
	}
	return fmt.Sprintf("%d lines, %d chars: %s", len(lines), len(output), head)
}

func nonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
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

func (m *TUIModel) activateFocus(next focus) {
	m.focus = next
	if next == focusInput {
		m.input.Focus()
		return
	}
	m.input.Blur()
}

func (m *TUIModel) cycleFocus() {
	if m.showDetail && m.selectedToolExec != nil {
		order := m.visibleFocusOrder()
		if len(order) == 0 {
			return
		}
		current := m.focusOrderIndex(order)
		if current < 0 {
			current = 0
		}
		m.activateFocus(order[(current+1)%len(order)])
		return
	}
	if m.isInRightHalf() {
		if m.focus == focusTrace && m.showStatus {
			m.activateFocus(focusStatus)
			return
		}
		m.activateFocus(focusTrace)
		return
	}
	order := m.visibleLeftFocusOrder()
	if len(order) == 0 {
		return
	}
	current := m.focusOrderIndex(order)
	if current < 0 {
		current = 0
	}
	m.activateFocus(order[(current+1)%len(order)])
}

func (m *TUIModel) visibleLeftFocusOrder() []focus {
	order := []focus{focusTranscript}
	if m.showDetail && m.selectedToolExec != nil {
		order = append(order, focusDetail)
	}
	order = append(order, focusInput)
	return order
}

func (m *TUIModel) visibleFocusOrder() []focus {
	order := []focus{focusTranscript}
	if m.showDetail && m.selectedToolExec != nil {
		order = append(order, focusDetail)
	}
	if m.showTrace {
		order = append(order, focusTrace)
		if m.showStatus {
			order = append(order, focusStatus)
		}
	}
	order = append(order, focusInput)
	return order
}

func (m *TUIModel) focusOrderIndex(order []focus) int {
	for i, candidate := range order {
		if candidate == m.focus {
			return i
		}
	}
	return -1
}

func (m *TUIModel) isInRightHalf() bool {
	return m.focus == focusTrace || m.focus == focusStatus
}

func (m *TUIModel) cycleFocusBack() {
	if m.isInRightHalf() {
		if m.focus == focusStatus {
			m.activateFocus(focusTrace)
			return
		}
		if m.showStatus {
			m.activateFocus(focusStatus)
			return
		}
		m.activateFocus(focusTrace)
		return
	}
	order := m.visibleLeftFocusOrder()
	if len(order) == 0 {
		return
	}
	current := m.focusOrderIndex(order)
	if current < 0 {
		current = 0
	}
	prev := current - 1
	if prev < 0 {
		prev = len(order) - 1
	}
	m.activateFocus(order[prev])
}

func (m *TUIModel) switchFocusHalf() {
	if m.focus == focusInput || m.focus == focusTranscript || m.focus == focusDetail {
		if m.showTrace {
			m.activateFocus(focusTrace)
		} else if m.focus == focusInput {
			m.activateFocus(focusTranscript)
		} else {
			m.activateFocus(focusInput)
		}
		return
	}
	if m.showDetail {
		m.activateFocus(focusDetail)
	} else {
		m.activateFocus(focusTranscript)
	}
}

func (m *TUIModel) resizeFocused(dxPct, dyPct int) {
	switch m.focus {
	case focusTranscript:
		m.leftPanePct = clampInt(m.leftPanePct+dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusTrace:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusStatus:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct-dyPct, 35, 85)
	}
}

func (m *TUIModel) handleBuiltInCommand(input string) tea.Cmd {
	if strings.EqualFold(strings.TrimSpace(input), "download this song") {
		return m.startDownloadCmd()
	}
	if strings.EqualFold(strings.TrimSpace(input), "/radio") {
		m.showRadioSelect = true
		return nil
	}
	return nil
}

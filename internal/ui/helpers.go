package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
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
		task := strings.TrimSpace(a.Task)
		if len(task) > 64 {
			task = task[:64] + "…"
		}
		return fmt.Sprintf("current: %s  %s  steps<=%d  %s",
			shortRunID(a.RunID), a.Model, a.MaxSteps, task)
	}
	if m.lastAgentNote != "" {
		return "last: " + m.lastAgentNote
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

	// Common case: collapse long multiline tool output into a single scan-friendly line.
	if strings.Count(output, "\n") >= 2 || len(output) > 160 {
		return compactToolSummary(output)
	}

	return TruncateOutput(output, false)
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

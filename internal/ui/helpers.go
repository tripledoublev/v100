package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

package ui

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *TUIModel) refreshDeviceStatus(now time.Time) {
	if !m.device.CheckedAt.IsZero() && time.Since(m.device.CheckedAt) < 30*time.Second {
		return
	}
	m.device = readDeviceStatus(now)
}

// deviceTickCmd sends a deviceTickMsg every 15 seconds to refresh battery status.
// This stays separate from the platform-specific battery readers.
func deviceTickCmd() tea.Cmd {
	return tea.Tick(15*time.Second, func(time.Time) tea.Msg { return deviceTickMsg{} })
}

func (m *TUIModel) deviceStatusLine() string {
	if !m.device.BatteryPresent {
		return "device: no battery"
	}
	line := "device: "
	if m.device.State != "" {
		line += m.device.State + " "
	}
	line += strconv.Itoa(m.device.Percent) + "%"
	return line
}

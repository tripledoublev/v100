//go:build !linux && !darwin
// +build !linux,!darwin

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

func deviceTickCmd() tea.Cmd {
	return tea.Tick(15*time.Second, func(time.Time) tea.Msg { return deviceTickMsg{} })
}

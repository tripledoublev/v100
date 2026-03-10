package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	lipgloss "github.com/charmbracelet/lipgloss"
)

// NewTUIModel creates a fresh TUI model.
func NewTUIModel() *TUIModel {
	ti := textinput.New()
	ti.Placeholder = "ask v100 to inspect, patch, or debug..."
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))
	ti.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9FAFB"))
	ti.Focus()
	ti.CharLimit = 4096

	tv := viewport.New(40, 20)
	trace := viewport.New(40, 20)

	m := &TUIModel{
		input:        ti,
		transcript:   tv,
		traceView:    trace,
		focus:        focusInput,
		showTrace:    true,
		showStatus:   true,
		statusMode:   "idle",
		statusLine:   "ready and waiting",
		runSummary:   "v100 run pending",
		leftPanePct:  66,
		tracePanePct: 70,
		radioURL:     "https://n04.radiojar.com/78cxy6wkxtzuv",
		radioPlayer:  detectRadioPlayer(),
		radioVolume:  60,
	}
	m.seedWelcomeContent()
	return m
}

package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	lipgloss "github.com/charmbracelet/lipgloss"
)

// NewTUIModel creates a fresh TUI model.
func NewTUIModel(codexAvailable, claudeAvailable bool) *TUIModel {
	ti := textinput.New()
	ti.Placeholder = "ask v100 to inspect, patch, or debug..."
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))
	ti.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9FAFB"))
	ti.Focus()
	ti.CharLimit = 4096

	tv := viewport.New(40, 20)
	trace := viewport.New(40, 20)
	detail := viewport.New(40, 20)

	m := &TUIModel{
		input:           ti,
		transcript:      tv,
		traceView:       trace,
		detailView:      detail,
		focus:           focusInput,
		showTrace:       true,
		showStatus:      true,
		showMetrics:     true,
		showDetail:      false,
		statusMode:      "idle",
		statusLine:      "ready and waiting",
		runSummary:      "v100 run pending",
		leftPanePct:     66,
		tracePanePct:    50,
		detailPanePct:   35,
		codexAvailable:  codexAvailable,
		claudeAvailable: claudeAvailable,
		radioURL:        "https://n04.radiojar.com/78cxy6wkxtzuv",
		radioPlayer:     detectRadioPlayer(),
		radioVolume:     60,
		imageRenderer:   NewImageRenderer(),
	}
	m.seedWelcomeContent()
	return m
}

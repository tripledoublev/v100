package ui

import (
	"os"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/i18n"
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
		StatusMode:      i18n.StatusIdle,
		statusMode:      i18n.StatusIdle.String(),
		statusLine:      i18n.T("status_ready"),
		runSummary:      "v100 run pending",
		leftPanePct:     66,
		tracePanePct:    50,
		detailPanePct:   35,
		codexAvailable:  codexAvailable,
		claudeAvailable: claudeAvailable,
		radioURL:        "https://n04.radiojar.com/78cxy6wkxtzuv",
		radioPlayer:     detectRadioPlayer(),
		radioVolume:     60,
		runReview:       realRunReview,
		imageRenderer:   NewImageRenderer(),
	}
	if wd, err := os.Getwd(); err == nil {
		m.WorkspacePath = wd
	}
	m.seedWelcomeContent()
	return m
}

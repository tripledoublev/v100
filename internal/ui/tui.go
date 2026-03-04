package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tripledoublev/v100/internal/core"
)

// TUI wraps the Bubble Tea program for the agent harness.
type TUI struct {
	program *tea.Program
	model   *TUIModel
}

// NewTUI creates a new TUI instance.
func NewTUI(submitFn func(string), useAltScreen bool, plainTTY bool) *TUI {
	if plainTTY {
		EnablePlainTTY()
	}
	m := NewTUIModel()
	m.SubmitFn = submitFn
	var p *tea.Program
	if useAltScreen {
		p = tea.NewProgram(m, tea.WithAltScreen())
	} else {
		p = tea.NewProgram(m)
	}
	return &TUI{program: p, model: m}
}

// SendEvent forwards a core event into the TUI message loop.
func (t *TUI) SendEvent(ev core.Event) {
	t.program.Send(EventMsg(ev))
}

// RequestConfirm shows a confirmation dialog and blocks until the user responds.
// Returns true if the user approved.
func (t *TUI) RequestConfirm(toolName, args string) bool {
	result := make(chan bool, 1)
	t.program.Send(RequestConfirmMsg{
		ToolName: toolName,
		Args:     args,
		Result:   result,
	})
	return <-result
}

// Run starts the TUI event loop (blocks until quit).
func (t *TUI) Run() error {
	_, err := t.program.Run()
	return err
}

// Quit terminates the TUI program.
func (t *TUI) Quit() {
	if t.model != nil {
		t.model.stopRadio()
	}
	t.program.Quit()
}

package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tripledoublev/v100/internal/core"
)

// TUI wraps the Bubble Tea program for the agent harness.
type TUI struct {
	program *tea.Program
	model   *TUIModel
	ready   chan struct{} // closed when Init() fires, signaling Send() is safe
}

// NewTUI creates a new TUI instance.
func NewTUI(submitFn func(string), useAltScreen bool, plainTTY bool) *TUI {
	if plainTTY {
		EnablePlainTTY()
	}
	ready := make(chan struct{})
	m := NewTUIModel()
	m.SubmitFn = submitFn
	m.onReady = func() { close(ready) }
	var p *tea.Program
	if useAltScreen {
		p = tea.NewProgram(m, tea.WithAltScreen())
	} else {
		p = tea.NewProgram(m)
	}
	return &TUI{program: p, model: m, ready: ready}
}

// WaitReady blocks until the TUI event loop is initialized and Send() is safe.
func (t *TUI) WaitReady() {
	<-t.ready
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

func (t *TUI) SetInterruptFn(fn func()) {
	if t.model != nil {
		t.model.InterruptFn = fn
	}
}

func (t *TUI) SetVerbose(v bool) {
	if t.model != nil {
		t.model.SetVerbose(v)
	}
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

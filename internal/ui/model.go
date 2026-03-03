package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
)

// focus identifies which pane is active.
type focus int

const (
	focusInput focus = iota
	focusTranscript
	focusTrace
)

// confirmState holds pending confirmation data.
type confirmState struct {
	active   bool
	toolName string
	args     string
	approved chan bool
}

// TUIModel is the Bubble Tea application model for the agent harness.
type TUIModel struct {
	width, height int

	transcript viewport.Model
	traceView  viewport.Model
	input      textinput.Model

	transcriptBuf strings.Builder
	traceBuf      strings.Builder
	planBuf       string

	focus       focus
	showTrace   bool
	pendConfirm *confirmState

	// callbacks
	SubmitFn func(string)
}

// Styles
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			PaddingLeft(1)

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	activePaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("212"))

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			PaddingLeft(1)

	confirmStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196")).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(1, 2)

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// NewTUIModel creates a fresh TUI model.
func NewTUIModel() TUIModel {
	ti := textinput.New()
	ti.Placeholder = "Enter message… (Ctrl+C to quit)"
	ti.Focus()
	ti.CharLimit = 4096

	tv := viewport.New(40, 20)
	trace := viewport.New(40, 20)

	return TUIModel{
		input:      ti,
		transcript: tv,
		traceView:  trace,
		focus:      focusInput,
		showTrace:  true,
	}
}

// EventMsg wraps a core.Event for the Bubble Tea message bus.
type EventMsg core.Event

// ConfirmMsg is sent when a confirmation result is available.
type ConfirmMsg struct {
	Approved bool
	confirm  *confirmState
}

// RequestConfirmMsg asks the TUI to show a confirmation dialog.
type RequestConfirmMsg struct {
	ToolName string
	Args     string
	Result   chan bool
}

func (m TUIModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()

	case EventMsg:
		m.appendEvent(core.Event(msg))

	case RequestConfirmMsg:
		m.pendConfirm = &confirmState{
			active:   true,
			toolName: msg.ToolName,
			args:     msg.Args,
			approved: msg.Result,
		}

	case ConfirmMsg:
		if msg.confirm != nil {
			msg.confirm.approved <- msg.Approved
		}
		m.pendConfirm = nil

	case tea.KeyMsg:
		// Global keys
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace
			m = m.recalcLayout()

		case "tab":
			m.cycleFocus()

		case "ctrl+y":
			if m.pendConfirm != nil && m.pendConfirm.active {
				confirm := m.pendConfirm
				return m, func() tea.Msg { return ConfirmMsg{Approved: true, confirm: confirm} }
			}

		case "ctrl+n":
			if m.pendConfirm != nil && m.pendConfirm.active {
				confirm := m.pendConfirm
				return m, func() tea.Msg { return ConfirmMsg{Approved: false, confirm: confirm} }
			}

		case "enter":
			if m.focus == focusInput && !m.pendConfirm.isActive() {
				val := strings.TrimSpace(m.input.Value())
				if val != "" {
					m.input.SetValue("")
					if m.SubmitFn != nil {
						go m.SubmitFn(val)
					}
				}
			}
		}
	}

	// Route key input to focused pane
	if _, ok := msg.(tea.KeyMsg); ok {
		switch m.focus {
		case focusTranscript:
			var cmd tea.Cmd
			m.transcript, cmd = m.transcript.Update(msg)
			cmds = append(cmds, cmd)
		case focusTrace:
			var cmd tea.Cmd
			m.traceView, cmd = m.traceView.Update(msg)
			cmds = append(cmds, cmd)
		case focusInput:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Always sync viewports on non-key messages
	if _, ok := msg.(tea.KeyMsg); !ok {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(msg)
		cmds = append(cmds, cmd)
		m.traceView, cmd = m.traceView.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m TUIModel) View() string {
	if m.width == 0 {
		return "Initializing…"
	}

	// Confirmation overlay
	if m.pendConfirm.isActive() {
		return m.confirmView()
	}

	header := headerStyle.Render("agent v0.0.1") + dimStyle.Render("  Tab:focus  Ctrl+T:trace  Ctrl+C:quit")

	inputBox := inputStyle.Width(m.width - 4).Render(m.input.View())

	inputHeight := 3
	headerHeight := 1
	remaining := m.height - headerHeight - inputHeight - 4

	if m.showTrace {
		leftW := (m.width - 3) * 2 / 3
		rightW := m.width - leftW - 3

		leftStyle := paneStyle
		rightStyle := paneStyle
		if m.focus == focusTranscript {
			leftStyle = activePaneStyle
		}
		if m.focus == focusTrace {
			rightStyle = activePaneStyle
		}

		m.transcript.Width = leftW - 2
		m.transcript.Height = remaining - 2
		m.traceView.Width = rightW - 2
		m.traceView.Height = remaining - 2

		left := leftStyle.Width(leftW).Height(remaining).Render(m.transcript.View())
		right := rightStyle.Width(rightW).Height(remaining).Render(m.traceView.View())
		panes := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		return lipgloss.JoinVertical(lipgloss.Left, header, panes, inputBox)
	}

	// Single pane
	tStyle := paneStyle
	if m.focus == focusTranscript {
		tStyle = activePaneStyle
	}
	m.transcript.Width = m.width - 4
	m.transcript.Height = remaining - 2
	pane := tStyle.Width(m.width - 2).Height(remaining).Render(m.transcript.View())
	return lipgloss.JoinVertical(lipgloss.Left, header, pane, inputBox)
}

func (m *TUIModel) appendEvent(ev core.Event) {
	ts := ev.TS.Format(time.TimeOnly)

	// Transcript pane (user-facing)
	switch ev.Type {
	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(fmt.Sprintf("\n[%s] YOU: %s\n", ts, p.Content))
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			m.transcriptBuf.WriteString(fmt.Sprintf("\n[%s] AGENT: %s\n", ts, p.Text))
		}
		for _, tc := range p.ToolCalls {
			m.transcriptBuf.WriteString(fmt.Sprintf("[%s]   → %s(%s)\n", ts, tc.Name, tc.ArgsJSON))
		}
	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "✓"
		if !p.OK {
			status = "✗"
		}
		out := p.Output
		if len(out) > 200 {
			out = out[:200] + "…"
		}
		m.transcriptBuf.WriteString(fmt.Sprintf("[%s]   %s %s: %s\n", ts, status, p.Name, out))
	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(fmt.Sprintf("\n[%s] ■ Run ended: %s (steps=%d tokens=%d)\n",
			ts, p.Reason, p.UsedSteps, p.UsedTokens))
	}

	// Trace pane (raw events)
	m.traceBuf.WriteString(fmt.Sprintf("[%s] %s\n", ts, ev.Type))

	m.transcript.SetContent(m.transcriptBuf.String())
	m.transcript.GotoBottom()
	m.traceView.SetContent(m.traceBuf.String())
	m.traceView.GotoBottom()
}

func (m *TUIModel) cycleFocus() {
	switch m.focus {
	case focusInput:
		m.focus = focusTranscript
		m.input.Blur()
	case focusTranscript:
		if m.showTrace {
			m.focus = focusTrace
		} else {
			m.focus = focusInput
			m.input.Focus()
		}
	case focusTrace:
		m.focus = focusInput
		m.input.Focus()
	}
}

func (m TUIModel) recalcLayout() TUIModel {
	return m
}

func (m TUIModel) confirmView() string {
	p := m.pendConfirm
	msg := fmt.Sprintf(
		"DANGEROUS TOOL CALL\n\nTool: %s\nArgs: %s\n\nApprove? Ctrl+Y = Yes   Ctrl+N = No",
		p.toolName, p.args,
	)
	box := confirmStyle.Render(msg)
	// Center the box
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (cs *confirmState) isActive() bool {
	return cs != nil && cs.active
}

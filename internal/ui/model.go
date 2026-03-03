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

	focus       focus
	showTrace   bool
	pendConfirm *confirmState

	// callbacks
	SubmitFn func(string)
}

// ── TUI styles ────────────────────────────────────────────────────────────────

var (
	tuiHeaderStyle = lipgloss.NewStyle().
			Foreground(clrPrimary).
			Bold(true)

	tuiHeaderDimStyle = lipgloss.NewStyle().
				Foreground(clrMuted)

	tuiPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#374151"))

	tuiActivePaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrPrimary)

	tuiInputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#374151")).
			PaddingLeft(1)

	tuiInputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrPrimary).
				PaddingLeft(1)

	tuiConfirmStyle = lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(clrDanger).
			Padding(1, 3)

	tuiTraceLabelStyle = lipgloss.NewStyle().
				Foreground(clrMuted).
				Italic(true)
)

// NewTUIModel creates a fresh TUI model.
func NewTUIModel() TUIModel {
	ti := textinput.New()
	ti.Placeholder = "message…"
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563"))
	ti.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9FAFB"))
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
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace

		case "tab":
			m.cycleFocus()

		case "ctrl+y":
			if m.pendConfirm.isActive() {
				confirm := m.pendConfirm
				return m, func() tea.Msg { return ConfirmMsg{Approved: true, confirm: confirm} }
			}

		case "ctrl+n":
			if m.pendConfirm.isActive() {
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
		return tuiHeaderDimStyle.Render("Initializing…")
	}

	if m.pendConfirm.isActive() {
		return m.confirmView()
	}

	// Header bar
	header := tuiHeaderStyle.Render("v100") +
		tuiHeaderDimStyle.Render("  Tab:focus  Ctrl+T:trace  Ctrl+C:quit")

	// Input box
	inputSt := tuiInputStyle
	if m.focus == focusInput {
		inputSt = tuiInputActiveStyle
	}
	inputBox := inputSt.Width(m.width - 4).Render(m.input.View())

	inputHeight := 3
	headerHeight := 1
	remaining := m.height - headerHeight - inputHeight - 4
	if remaining < 4 {
		remaining = 4
	}

	if m.showTrace {
		leftW := (m.width - 3) * 2 / 3
		rightW := m.width - leftW - 3

		leftSt := tuiPaneStyle
		rightSt := tuiPaneStyle
		if m.focus == focusTranscript {
			leftSt = tuiActivePaneStyle
		}
		if m.focus == focusTrace {
			rightSt = tuiActivePaneStyle
		}

		m.transcript.Width = leftW - 2
		m.transcript.Height = remaining - 2
		m.traceView.Width = rightW - 2
		m.traceView.Height = remaining - 2

		left := leftSt.Width(leftW).Height(remaining).Render(m.transcript.View())
		right := rightSt.Width(rightW).Height(remaining).Render(
			tuiTraceLabelStyle.Render("trace") + "\n" + m.traceView.View(),
		)
		panes := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		return lipgloss.JoinVertical(lipgloss.Left, header, panes, inputBox)
	}

	// Single pane
	tSt := tuiPaneStyle
	if m.focus == focusTranscript {
		tSt = tuiActivePaneStyle
	}
	m.transcript.Width = m.width - 4
	m.transcript.Height = remaining - 2
	pane := tSt.Width(m.width - 2).Height(remaining).Render(m.transcript.View())
	return lipgloss.JoinVertical(lipgloss.Left, header, pane, inputBox)
}

func (m *TUIModel) appendEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Format(time.TimeOnly))

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(
			stylePrimary.Render("v100") +
				styleMuted.Render("  run "+ev.RunID[:8]+"  "+p.Provider+" · "+p.Model) +
				"\n\n",
		)

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"\n%s  %s  %s\n",
			ts, styleUser.Render("you"), p.Content,
		))

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s  %s  %s\n",
				ts, styleAssistant.Render("v100"), p.Text,
			))
		}
		for _, tc := range p.ToolCalls {
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"           %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+tc.ArgsJSON+")"),
			))
		}

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		icon, nameStr := styleOK.Render("✓"), styleOK.Render(p.Name)
		if !p.OK {
			icon, nameStr = styleFail.Render("✗"), styleFail.Render(p.Name)
		}
		out := p.Output
		if len(out) > 200 {
			out = out[:200] + "…"
		}
		out = strings.ReplaceAll(out, "\n", " ↵ ")
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"           %s %s  %s  %s\n",
			icon, nameStr,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			out,
		))

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"\n%s\n",
			styleMuted.Render(fmt.Sprintf("■ run ended: %s  steps=%d  tokens=%d",
				p.Reason, p.UsedSteps, p.UsedTokens)),
		))

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"\n%s  %s\n",
			styleFail.Render("✗ error"),
			styleFail.Render(p.Error),
		))
	}

	// Trace pane: compact colored event log
	m.traceBuf.WriteString(
		styleMuted.Render(ev.TS.Format(time.TimeOnly)) + "  " +
			styleRunID.Render(string(ev.Type)) + "\n",
	)

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

func (m TUIModel) confirmView() string {
	p := m.pendConfirm
	content := styleDanger.Render("⚠  DANGEROUS TOOL CALL") + "\n\n" +
		styleMuted.Render("Tool: ") + styleTool.Render(p.toolName) + "\n" +
		styleMuted.Render("Args: ") + p.args + "\n\n" +
		styleWarn.Render("Approve?") + "  " +
		styleOK.Render("Ctrl+Y") + " yes   " +
		styleFail.Render("Ctrl+N") + " no"
	box := tuiConfirmStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (cs *confirmState) isActive() bool {
	return cs != nil && cs.active
}

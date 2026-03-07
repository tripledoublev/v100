package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"

	"github.com/tripledoublev/v100/internal/core"
)

// focus identifies which pane is active.
type focus int

const (
	focusInput focus = iota
	focusTranscript
	focusTrace
	focusStatus
)

// confirmState holds pending confirmation data.
type confirmState struct {
	active   bool
	toolName string
	args     string
	approved chan bool
}

// copyTarget records a copy-icon line and its associated content.
type copyTarget struct {
	lineNo  int
	content string
}

type agentFrame struct {
	RunID    string
	CallID   string
	Task     string
	Model    string
	MaxSteps int
	Tools    int
	Started  time.Time
}

// TUIModel is the Bubble Tea application model for the agent harness.
type TUIModel struct {
	width, height int

	transcript viewport.Model
	traceView  viewport.Model
	input      textinput.Model

	transcriptBuf strings.Builder
	traceBuf      strings.Builder

	focus         focus
	showTrace     bool
	showStatus    bool
	pendConfirm   *confirmState
	statusMode    string
	statusLine    string
	statusTick    int
	runSummary    string
	leftPanePct   int
	tracePanePct  int
	radioURL      string
	radioPlayer   string
	radioVolume   int
	radioPlaying  bool
	radioWave     string
	radioErr      string
	radioStep     int
	radioCmd      *exec.Cmd
	radioArtist   string
	radioTitle    string
	radioLastPoll time.Time

	copyTargets    []copyTarget
	plainBuf       strings.Builder // plain-text transcript for full-copy
	inSubAgent     int             // nesting depth; >0 means inside agent.start..agent.end
	traceStepCount int             // running step count for trace pane
	activeAgents   []agentFrame
	agentDoneCount int
	agentFailCount int
	lastAgentNote  string

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
			BorderForeground(lipgloss.Color("#374151"))

	tuiInputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrPrimary)

	tuiConfirmStyle = lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(clrDanger).
			Padding(1, 3)

	tuiTraceLabelStyle = lipgloss.NewStyle().
				Foreground(clrMuted).
				Italic(true)

	tuiStatusLabelStyle = lipgloss.NewStyle().
				Foreground(clrMuted).
				Italic(true)

	tuiCopyIconStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#374151"))
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

type radioTickMsg struct{}
type radioNowPlayingMsg struct {
	Artist string
	Title  string
	Err    string
}
type downloadDoneMsg struct {
	artist string
	title  string
	err    string
}

func (m *TUIModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.WindowSize(),
		func() tea.Msg { return tea.ClearScreen() },
		radioTickCmd(),
		tea.EnableMouseCellMotion,
	)
}

func (m *TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cmds = append(cmds, func() tea.Msg { return tea.ClearScreen() })

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

	case radioTickMsg:
		if cmd := m.onRadioTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, radioTickCmd())

	case radioNowPlayingMsg:
		if msg.Err != "" {
			m.radioErr = msg.Err
		} else {
			m.radioArtist = strings.TrimSpace(msg.Artist)
			m.radioTitle = strings.TrimSpace(msg.Title)
		}

	case downloadDoneMsg:
		if msg.err != "" {
			m.radioErr = msg.err
			m.statusMode = "error"
			m.statusLine = "download failed"
		} else {
			m.radioErr = ""
			m.statusMode = "idle"
			m.statusLine = "downloaded: " + strings.TrimSpace(msg.artist+" - "+msg.title)
		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.handleMouseClick(msg.X, msg.Y)
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.stopRadio()
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace
			if !m.showTrace && (m.focus == focusTrace || m.focus == focusStatus) {
				m.focus = focusTranscript
				m.input.Blur()
			}

		case "ctrl+s":
			m.showStatus = !m.showStatus
			if !m.showStatus && m.focus == focusStatus {
				m.focus = focusTrace
			}
		case "ctrl+a":
			if err := copyToClipboard(m.plainBuf.String()); err != nil {
				m.statusLine = "copy failed: " + err.Error()
				m.statusMode = "error"
			} else {
				m.statusLine = "full transcript copied to clipboard"
			}
		case "ctrl+r":
			m.toggleRadio()
		case "]":
			m.adjustRadioVolume(5)
		case "[":
			m.adjustRadioVolume(-5)

		case "tab":
			m.cycleFocus()
		case "shift+tab":
			m.cycleFocusBack()
		case "ctrl+shift+tab", "ctrl+tab", "ctrl+pgup", "ctrl+pgdown":
			m.switchFocusHalf()
		case "ctrl+\\":
			m.switchFocusHalf()
		case "shift+left":
			m.resizeFocused(-4, 0)
		case "shift+right":
			m.resizeFocused(4, 0)
		case "shift+up":
			m.resizeFocused(0, 4)
		case "shift+down":
			m.resizeFocused(0, -4)

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
				val := sanitizeInputNoise(strings.TrimSpace(m.input.Value()))
				if val != "" {
					m.input.SetValue("")
					if cmd := m.handleBuiltInCommand(val); cmd != nil {
						cmds = append(cmds, cmd)
						break
					}
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
		case focusStatus:
			// status pane is informational (no per-key updates needed)
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

func (m *TUIModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		// Fallback for terminals that don't deliver an initial size immediately.
		w, h := envSizeFallback()
		if w > 0 && h > 0 {
			m.width = w
			m.height = h
		}
	}
	if m.width <= 0 || m.height <= 0 {
		return tuiHeaderDimStyle.Render("Initializing terminal size...")
	}

	if m.pendConfirm.isActive() {
		return m.confirmView()
	}

	// Header bar with responsive width to avoid terminal soft-wrap.
	headerHint := "  Tab:focus  Shift+Tab:back  Ctrl+PgUp/PgDn:half  Shift+Arrows:resize  Ctrl+T:trace  Ctrl+S:status  Ctrl+A:copy all  Ctrl+C:quit"
	if m.width < 130 {
		headerHint = "  Tab:focus  Ctrl+PgUp/PgDn:half  Ctrl+T:trace  Ctrl+A:copy all  Ctrl+C:quit"
	}
	if m.width < 100 {
		headerHint = "  Tab:focus  Ctrl+PgUp/PgDn:half  Ctrl+A:copy  Ctrl+C:quit"
	}
	header := tuiHeaderStyle.Render("v100") + tuiHeaderDimStyle.Render(headerHint)

	// Input box
	inputSt := tuiInputStyle
	if m.focus == focusInput {
		inputSt = tuiInputActiveStyle
	}
	inputBox := inputSt.Width(m.width - 2).Render(m.input.View())

	inputHeight := lipgloss.Height(inputBox)
	headerHeight := lipgloss.Height(header)
	// JoinVertical uses '\n' as a line terminator between elements, not an extra row.
	remaining := m.height - headerHeight - inputHeight
	if remaining < 4 {
		remaining = 4
	}

	if m.showTrace {
		// Each pane has a 1-char border on each side (2 per pane) + 1-char gap = 5 overhead.
		// leftW and rightW are inner content widths; outer = inner + 2.
		total := m.width - 5
		leftW := total * m.leftPanePct / 100
		if leftW < 38 {
			leftW = 38
		}
		rightW := total - leftW
		if rightW < 24 {
			rightW = 24
			leftW = total - rightW
		}

		leftSt := tuiPaneStyle
		rightSt := tuiPaneStyle
		if m.focus == focusTranscript {
			leftSt = tuiActivePaneStyle
		}
		if m.focus == focusTrace {
			rightSt = tuiActivePaneStyle
		}

		paneInnerH := remaining - 2
		statusH := 0
		traceH := paneInnerH
		if m.showStatus {
			rightBudget := remaining - 4
			traceH = rightBudget * m.tracePanePct / 100
			if traceH < 4 {
				traceH = 4
			}
			statusH = rightBudget - traceH
			if statusH < 2 {
				statusH = 2
				traceH = rightBudget - statusH
			}
		}

		m.transcript.Width = leftW - 4
		m.transcript.Height = paneInnerH
		m.traceView.Width = rightW - 4
		m.traceView.Height = traceH - 2

		left := leftSt.Width(leftW).Height(paneInnerH).Render(m.transcript.View())
		tracePane := rightSt.Width(rightW).Height(traceH).Render(
			tuiTraceLabelStyle.Render("trace") + "\n" + m.traceView.View(),
		)
		rightCol := tracePane
		if m.showStatus {
			statusSt := tuiPaneStyle
			if m.focus == focusStatus {
				statusSt = tuiActivePaneStyle
			}
			statusPane := statusSt.Width(rightW).Height(statusH).Render(m.statusView(rightW, statusH))
			rightCol = lipgloss.JoinVertical(lipgloss.Left, tracePane, statusPane)
		}

		panes := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", rightCol)
		view := lipgloss.JoinVertical(lipgloss.Left, header, panes, inputBox)
		return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
	}

	// Single pane
	tSt := tuiPaneStyle
	if m.focus == focusTranscript {
		tSt = tuiActivePaneStyle
	}
	paneInnerH := remaining - 2
	m.transcript.Width = m.width - 4
	m.transcript.Height = paneInnerH
	pane := tSt.Width(m.width - 2).Height(paneInnerH).Render(m.transcript.View())
	view := lipgloss.JoinVertical(lipgloss.Left, header, pane, inputBox)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
}

func (m *TUIModel) appendEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Format(time.TimeOnly))
	m.updateStatusFromEvent(ev)
	sub := len(m.activeAgents) > 0

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			m.transcriptBuf.WriteString(
				stylePrimary.Render("v100") +
					styleMuted.Render("  run "+ev.RunID[:8]+"  "+p.Provider+" · "+p.Model) +
					"\n\n",
			)
			m.plainBuf.WriteString(fmt.Sprintf("v100  run %s  %s · %s\n\n", ev.RunID[:8], p.Provider, p.Model))
		}

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			wrapped := m.wrapPlainForTranscript(p.Content)
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s  %s  %s\n",
				ts, styleUser.Render("you"), wrapped,
			))
			iconLine := strings.Count(m.transcriptBuf.String(), "\n")
			m.transcriptBuf.WriteString("           " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
			m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: p.Content})
			m.plainBuf.WriteString(fmt.Sprintf("\nyou: %s\n", p.Content))
		}

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			// Sub-agent model response: show only a compact summary line
			if p.Text != "" {
				summary := p.Text
				if len(summary) > 120 {
					summary = summary[:120] + "…"
				}
				summary = strings.ReplaceAll(summary, "\n", " ")
				m.transcriptBuf.WriteString(fmt.Sprintf(
					"       %s  %s\n",
					styleMuted.Render("◆"), styleMuted.Render(summary),
				))
			}
			break
		}
		m.focus = focusTranscript
		m.input.Blur()
		if p.Text != "" {
			rendered := m.renderMarkdownForPane(p.Text)
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s  %s\n%s\n",
				ts, styleAssistant.Render("v100"), rendered,
			))
			iconLine := strings.Count(m.transcriptBuf.String(), "\n")
			m.transcriptBuf.WriteString("    " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
			m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: p.Text})
			m.plainBuf.WriteString(fmt.Sprintf("\nv100: %s\n", p.Text))
		}
		for _, tc := range p.ToolCalls {
			args := m.wrapPlainForTranscript(tc.ArgsJSON)
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"           %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+args+")"),
			))
		}

	case core.EventToolCall:
		if sub {
			// Sub-agent tool calls: single compact line
			var p core.ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			agent := m.currentAgentLabel()
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"       %s %s  %s %s\n",
				styleMuted.Render("◆"),
				styleMuted.Render(agent),
				styleTool.Render(p.Name),
				styleMuted.Render("…"),
			))
		}

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			// Sub-agent tool results: one short status line
			icon := styleOK.Render("✓")
			if !p.OK {
				icon = styleFail.Render("✗")
			}
			agent := m.currentAgentLabel()
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"       %s %s  %s %s\n",
				styleMuted.Render("◆"),
				styleMuted.Render(agent),
				icon,
				styleMuted.Render(p.Name),
			))
			break
		}
		icon, nameStr := styleOK.Render("✓"), styleOK.Render(p.Name)
		if !p.OK {
			icon, nameStr = styleFail.Render("✗"), styleFail.Render(p.Name)
		}
		out := p.Output
		if len(out) > 200 {
			out = out[:200] + "…"
		}
		out = strings.ReplaceAll(out, "\n", " ↵ ")
		out = m.wrapPlainForTranscript(out)
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"           %s %s  %s  %s\n",
			icon, nameStr,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			out,
		))

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s\n",
				styleMuted.Render(fmt.Sprintf("■ run ended: %s  steps=%d  tokens=%d",
					p.Reason, p.UsedSteps, p.UsedTokens)),
			))
			m.plainBuf.WriteString(fmt.Sprintf("\n■ run ended: %s  steps=%d  tokens=%d\n", p.Reason, p.UsedSteps, p.UsedTokens))
		}

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"       %s %s\n",
				styleMuted.Render("◆"), styleFail.Render("error: "+p.Error),
			))
		} else {
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s  %s\n",
				styleFail.Render("✗ error"),
				styleFail.Render(p.Error),
			))
			m.plainBuf.WriteString(fmt.Sprintf("\nerror: %s\n", p.Error))
		}

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.activeAgents = append(m.activeAgents, agentFrame{
			RunID:    p.AgentRunID,
			CallID:   p.ParentCallID,
			Task:     p.Task,
			Model:    p.Model,
			MaxSteps: p.MaxSteps,
			Tools:    len(p.Tools),
			Started:  ev.TS,
		})
		m.inSubAgent = len(m.activeAgents)
		task := p.Task
		if len(task) > 80 {
			task = task[:80] + "…"
		}
		label := "◆ agent▸"
		if strings.TrimSpace(p.Agent) != "" {
			label = "◆ dispatch▸ " + p.Agent
		}
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"\n%s  %s  %s  %s\n",
			ts,
			styleInfo.Render(label),
			styleMuted.Render(shortRunID(p.AgentRunID)),
			styleMuted.Render(fmt.Sprintf("%s  tools=%d max_steps=%d", p.Model, len(p.Tools), p.MaxSteps)),
		))
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"       %s\n",
			styleMuted.Render(task),
		))
		m.plainBuf.WriteString(fmt.Sprintf("\n◆ agent: %s\n", task))

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.removeActiveAgent(p.AgentRunID)
		m.inSubAgent = len(m.activeAgents)
		okLabel := "◆ done"
		failLabel := "◆ agent failed"
		if strings.TrimSpace(p.Agent) != "" {
			okLabel = "◆ dispatch done"
			failLabel = "◆ dispatch failed"
		}
		if p.OK {
			m.agentDoneCount++
			// Show a trimmed result summary
			result := p.Result
			if len(result) > 200 {
				result = result[:200] + "…"
			}
			result = strings.ReplaceAll(result, "\n", " ")
			m.lastAgentNote = fmt.Sprintf("%s ok  steps=%d tok=%d", shortRunID(p.AgentRunID), p.UsedSteps, p.UsedTokens)
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"%s  %s  %s  %s  %s  %s\n",
				ts, styleOK.Render(okLabel),
				styleMuted.Render(shortRunID(p.AgentRunID)),
				styleMuted.Render(fmt.Sprintf("steps=%d tok=%d", p.UsedSteps, p.UsedTokens)),
				styleMuted.Render(fmt.Sprintf("$%.4f", p.CostUSD)),
				styleMuted.Render(result),
			))
			m.plainBuf.WriteString(fmt.Sprintf("◆ agent done (steps=%d tokens=%d): %s\n", p.UsedSteps, p.UsedTokens, result))
		} else {
			m.agentFailCount++
			result := p.Result
			if len(result) > 120 {
				result = result[:120] + "…"
			}
			result = strings.ReplaceAll(result, "\n", " ")
			m.lastAgentNote = fmt.Sprintf("%s failed", shortRunID(p.AgentRunID))
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"%s  %s  %s  %s\n",
				ts, styleFail.Render(failLabel),
				styleMuted.Render(shortRunID(p.AgentRunID)),
				styleMuted.Render(result),
			))
			m.plainBuf.WriteString(fmt.Sprintf("◆ agent failed: %s\n", result))
		}
	}

	// Trace pane: compact, semantic event stream with per-tool cues.
	m.traceBuf.WriteString(m.renderTraceEvent(ev) + "\n")

	m.transcript.SetContent(m.transcriptBuf.String())
	m.transcript.GotoBottom()
	m.traceView.SetContent(m.traceBuf.String())
	m.traceView.GotoBottom()
}

func (m *TUIModel) cycleFocus() {
	if m.isInRightHalf() {
		if m.focus == focusTrace && m.showStatus {
			m.focus = focusStatus
			m.input.Blur()
			return
		}
		m.focus = focusTrace
		m.input.Blur()
		return
	}

	// Left half: transcript <-> input
	if m.focus == focusInput {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	m.focus = focusInput
	m.input.Focus()
}

func (m *TUIModel) cycleFocusBack() {
	if m.isInRightHalf() {
		if m.focus == focusStatus {
			m.focus = focusTrace
			m.input.Blur()
			return
		}
		if m.showStatus {
			m.focus = focusStatus
			m.input.Blur()
			return
		}
		m.focus = focusTrace
		m.input.Blur()
		return
	}

	// Left half: input <-> transcript
	if m.focus == focusInput {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	m.focus = focusInput
	m.input.Focus()
}

func (m *TUIModel) switchFocusHalf() {
	if m.isInRightHalf() {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	if m.showTrace {
		m.focus = focusTrace
		m.input.Blur()
		return
	}
	m.focus = focusTranscript
	m.input.Blur()
}

func (m *TUIModel) isInRightHalf() bool {
	return m.focus == focusTrace || m.focus == focusStatus
}

func (m *TUIModel) resizeFocused(dxPct, dyPct int) {
	switch m.focus {
	case focusTranscript:
		m.leftPanePct = clampInt(m.leftPanePct+dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusTrace:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusStatus:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct-dyPct, 35, 85)
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

func (m *TUIModel) seedWelcomeContent() {
	now := time.Now().Format("2006-01-02 15:04:05")

	m.transcriptBuf.WriteString(stylePrimary.Render("control deck") + styleMuted.Render(" • session ready • "+now) + "\n\n")

	m.transcriptBuf.WriteString(styleBold.Render("Quick Starts") + "\n")
	m.transcriptBuf.WriteString(styleMuted.Render("1.") + " map this repo and explain architecture\n")
	m.transcriptBuf.WriteString(styleMuted.Render("2.") + " find likely bugs and propose fixes\n")
	m.transcriptBuf.WriteString(styleMuted.Render("3.") + " add a feature and patch files\n\n")

	m.transcriptBuf.WriteString(styleBold.Render("Controls") + "\n")
	m.transcriptBuf.WriteString(styleMuted.Render("Enter") + " send  " + styleMuted.Render("Tab") + " focus  " + styleMuted.Render("Ctrl+Shift+Tab") + " half  " + styleMuted.Render("Ctrl+T") + " trace  " + styleMuted.Render("Ctrl+S") + " status  " + styleMuted.Render("Ctrl+C") + " quit\n\n")

	m.transcriptBuf.WriteString(styleMuted.Render("Type a task below and press Enter."))

	m.traceBuf.WriteString(tuiTraceLabelStyle.Render("trace stream") + "\n")
	m.traceBuf.WriteString(styleMuted.Render("waiting for events...") + "\n\n")
	m.traceBuf.WriteString(styleMuted.Render("run_start  model response  tool_call  tool_result  run_end"))

	m.transcript.SetContent(m.transcriptBuf.String())
	m.traceView.SetContent(m.traceBuf.String())
}

func (m *TUIModel) renderMarkdownForPane(text string) string {
	src := strings.TrimSpace(text)
	if src == "" {
		return ""
	}

	width := m.width - 8
	if m.showTrace {
		width = (m.width-3)*2/3 - 6
	}
	if width < 40 {
		width = 40
	}
	if width > 120 {
		width = 120
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	return strings.TrimRight(out, "\n")
}

func (m *TUIModel) wrapPlainForTranscript(text string) string {
	w := m.transcriptWrapWidth()
	if w < 24 {
		return text
	}
	return wrap.String(text, w)
}

func (m *TUIModel) transcriptWrapWidth() int {
	if m.width <= 0 {
		return 80
	}
	if m.showTrace {
		leftW := (m.width - 3) * 2 / 3
		return leftW - 8
	}
	return m.width - 8
}

func (m *TUIModel) statusView(width, height int) string {
	line := m.statusLine
	w := width - 6
	if w < 12 {
		w = 12
	}
	line = wrap.String(line, w)

	lines := []string{
		tuiStatusLabelStyle.Render("status"),
		stylePrimary.Render(wrap.String(m.runSummary, w)),
		styleBold.Render(strings.ToUpper(m.statusMode)),
		styleMuted.Render(line),
		"",
		styleMuted.Render(fmt.Sprintf("sub-agents: active=%d done=%d failed=%d",
			len(m.activeAgents), m.agentDoneCount, m.agentFailCount)),
		styleMuted.Render(m.subAgentStatusLine()),
		"",
		styleMuted.Render("radio") + " " + m.radioStateLine(),
		styleMuted.Render("feed: " + m.radioURL),
	}
	if m.radioArtist != "" || m.radioTitle != "" {
		lines = append(lines, stylePrimary.Render("now: "+strings.TrimSpace(m.radioArtist+" - "+m.radioTitle)))
	}
	if m.radioWave != "" {
		wave := m.renderWaveForWidth(w)
		lines = append(lines, styleInfo.Render(centerToWidth(wave, w)))
	}
	if m.radioErr != "" {
		lines = append(lines, styleFail.Render(m.radioErr))
	}

	// Keep content bounded to pane height to avoid stale lines after resize.
	contentH := height - 2 // border consumes 2 lines in rounded style
	if contentH < 1 {
		contentH = 1
	}
	if len(lines) > contentH {
		lines = lines[:contentH]
	}
	return strings.Join(lines, "\n")
}

func (m *TUIModel) renderTraceEvent(ev core.Event) string {
	sep := styleMuted.Render("┊")
	indent := ""
	if m.inSubAgent > 0 && ev.Type != core.EventAgentStart && ev.Type != core.EventAgentEnd {
		indent = styleMuted.Render("· ")
	}

	switch ev.Type {
	case core.EventRunStart:
		return sep + "  " + indent + styleInfo.Render("▶▶") + "  " + styleMuted.Render("run")
	case core.EventUserMsg:
		return sep + "  " + indent + styleUser.Render(">>") + "  " + styleUser.Render("you")
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		latStr := ""
		if p.DurationMS > 0 {
			latStr = "  " + latencyStyle(p.DurationMS).Render(fmt.Sprintf("[%dms]", p.DurationMS))
		}
		if len(p.ToolCalls) > 0 {
			return sep + "  " + indent + styleAssistant.Render("~~") + "  " + styleAssistant.Render(fmt.Sprintf("model  +%d", len(p.ToolCalls))) + latStr
		}
		return sep + "  " + indent + styleAssistant.Render("~~") + "  " + styleAssistant.Render("model") + latStr
	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return sep + "  " + indent + styleWarn.Render(toolGlyph(p.Name)) + "  " + styleWarn.Render(p.Name)
	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		dur := styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS))
		if p.OK {
			return sep + "  " + indent + styleOK.Render("✓") + "  " + styleOK.Render(p.Name) + "  " + dur
		}
		return sep + "  " + indent + styleFail.Render("✗") + "  " + styleFail.Render(p.Name) + "  " + dur + "  " + styleFail.Render("[err]")
	case core.EventRunError:
		return sep + "  " + indent + styleFail.Render("!!") + "  " + styleFail.Render("error")
	case core.EventRunEnd:
		return sep + "  " + styleMuted.Render("■■") + "  " + styleMuted.Render("end")
	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "dispatch:" + p.Agent
		}
		return sep + "  " + styleInfo.Render("◆▶") + "  " + styleInfo.Render(
			fmt.Sprintf("%s %s  %s  max=%d", label, shortRunID(p.AgentRunID), p.Model, p.MaxSteps))
	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "dispatch:" + p.Agent
		}
		if p.OK {
			return sep + "  " + styleOK.Render("◆■") + "  " + styleOK.Render(
				fmt.Sprintf("%s %s done  steps=%d tok=%d", label, shortRunID(p.AgentRunID), p.UsedSteps, p.UsedTokens))
		}
		return sep + "  " + styleFail.Render("◆■") + "  " + styleFail.Render(
			fmt.Sprintf("%s %s fail", label, shortRunID(p.AgentRunID)))
	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.traceStepCount = p.StepNumber
		hdr := sep + "  " + stylePrimary.Render(fmt.Sprintf("── step %d ──────────────────────", p.StepNumber))
		detail := sep + "     " + styleMuted.Render(fmt.Sprintf("tok=%dk  $%.4f  %d tools  %dms",
			p.InputTokens/1000, p.CostUSD, p.ToolCalls, p.DurationMS))
		return hdr + "\n" + detail
	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return sep + "  " + styleInfo.Render("⊘⊘") + "  " + styleInfo.Render(
			fmt.Sprintf("compress  %d→%d msgs  ~%dk→%dk tok",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000))
	default:
		return sep + "  " + indent + styleMuted.Render("::") + "  " + styleMuted.Render(string(ev.Type))
	}
}

// latencyStyle returns a style colored by latency bracket.
func latencyStyle(ms int64) lipgloss.Style {
	if ms < 500 {
		return styleLatFast
	}
	if ms <= 2000 {
		return styleLatMed
	}
	return styleLatSlow
}

// toolGlyph returns a short, non-emoji Unicode label for a tool name.
func toolGlyph(name string) string {
	switch name {
	case "fs_list":
		return "ls"
	case "fs_read":
		return " <"
	case "fs_write":
		return " >"
	case "fs_mkdir":
		return " +"
	case "project_search":
		return "//"
	case "patch_apply":
		return "~~"
	case "git_status":
		return "g?"
	case "git_diff":
		return "g~"
	case "git_commit":
		return "g+"
	case "git_push":
		return "g^"
	case "curl_fetch":
		return " @"
	case "sh":
		return " $"
	case "agent":
		return " ◆"
	case "dispatch":
		return " ↗"
	case "orchestrate":
		return " ⊕"
	case "blackboard_read":
		return " b<"
	case "blackboard_write":
		return " b>"
	default:
		return "::"
	}
}

func (m *TUIModel) updateStatusFromEvent(ev core.Event) {
	m.statusTick++
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "idle"
		m.statusLine = "booted and listening"
		runShort := ev.RunID
		if len(runShort) > 8 {
			runShort = runShort[:8]
		}
		m.runSummary = fmt.Sprintf("v100 run %s  %s · %s", runShort, p.Provider, p.Model)
	case core.EventUserMsg:
		m.statusMode = "thinking"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"digesting your request",
			"scanning context and constraints",
			"planning a clean approach",
		})
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if len(p.ToolCalls) > 0 {
			m.statusMode = "tooling"
			m.statusLine = pickStatusLine(m.statusTick, []string{
				"looking at code",
				"searching repo",
				"making pancakes",
				"running tools for signal",
			})
		} else {
			m.statusMode = "idle"
			m.statusLine = pickStatusLine(m.statusTick, []string{
				"ready for your next move",
				"response delivered",
				"standing by",
			})
		}
	case core.EventToolCall:
		m.statusMode = "tooling"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"executing tool call",
			"collecting evidence",
			"digging through files",
		})
	case core.EventToolResult:
		m.statusMode = "thinking"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"stitching tool outputs together",
			"cross-checking findings",
			"digesting information",
		})
	case core.EventRunError:
		m.statusMode = "error"
		m.statusLine = "hit an error; check transcript"
	case core.EventRunEnd:
		m.statusMode = "idle"
		m.statusLine = "run ended"
	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "tooling"
		m.statusLine = fmt.Sprintf("sub-agent %s running (%s)", shortRunID(p.AgentRunID), p.Model)
	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "thinking"
		if p.OK {
			m.statusLine = fmt.Sprintf("sub-agent %s completed", shortRunID(p.AgentRunID))
		} else {
			m.statusLine = fmt.Sprintf("sub-agent %s failed", shortRunID(p.AgentRunID))
		}
	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "idle"
		m.statusLine = fmt.Sprintf("step %d done — %d tools, %dms, $%.4f",
			p.StepNumber, p.ToolCalls, p.DurationMS, p.CostUSD)
	case core.EventCompress:
		m.statusMode = "thinking"
		m.statusLine = "context compressed"
	}
}

func pickStatusLine(n int, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[n%len(lines)]
}

func (cs *confirmState) isActive() bool {
	return cs != nil && cs.active
}

func envSizeFallback() (int, int) {
	w, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	h, _ := strconv.Atoi(os.Getenv("LINES"))
	return w, h
}

func sanitizeInputNoise(s string) string {
	if strings.HasPrefix(s, "]11;rgb:") || strings.HasPrefix(s, "\x1b]11;rgb:") {
		return ""
	}
	return s
}

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

func (m *TUIModel) currentAgentLabel() string {
	if len(m.activeAgents) == 0 {
		return "agent"
	}
	return shortRunID(m.activeAgents[len(m.activeAgents)-1].RunID)
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

func radioTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return radioTickMsg{} })
}

func (m *TUIModel) onRadioTick() tea.Cmd {
	if !m.radioPlaying {
		m.radioWave = ""
		return nil
	}
	m.radioStep++
	levels := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"}
	var b strings.Builder
	for i := 0; i < 96; i++ {
		idx := (m.radioStep + i*2 + (i % 7)) % len(levels)
		b.WriteString(levels[idx])
	}
	m.radioWave = b.String()
	// Poll now-playing metadata conservatively to avoid rate limits.
	if m.radioLastPoll.IsZero() || time.Since(m.radioLastPoll) >= 30*time.Second {
		m.radioLastPoll = time.Now()
		return fetchNowPlayingCmd(m.radioStationID())
	}
	return nil
}

func (m *TUIModel) renderWaveForWidth(width int) string {
	if width < 8 {
		return "♪"
	}
	target := width
	if target < 6 {
		target = 6
	}
	if len(m.radioWave) >= target {
		return m.radioWave[:target]
	}
	if len(m.radioWave) == 0 {
		return "♪"
	}
	repeats := (target + len(m.radioWave) - 1) / len(m.radioWave)
	wave := strings.Repeat(m.radioWave, repeats)
	return wave[:target]
}

func centerToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	if lipgloss.Width(s) >= width {
		return s
	}
	pad := width - lipgloss.Width(s)
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

func (m *TUIModel) toggleRadio() {
	if m.radioPlaying {
		m.stopRadio()
		return
	}
	m.startRadio()
}

func (m *TUIModel) adjustRadioVolume(delta int) {
	m.radioVolume = clampInt(m.radioVolume+delta, 0, 100)
	if m.radioPlaying {
		m.stopRadio()
		m.startRadio()
	}
}

func (m *TUIModel) radioStateLine() string {
	state := "idle"
	if m.radioPlaying {
		state = "playing"
	}
	return fmt.Sprintf("%s  vol=%d%%  Ctrl+R play/stop  [/] volume", state, m.radioVolume)
}

func (m *TUIModel) startRadio() {
	m.radioErr = ""
	if m.radioURL == "" {
		m.radioURL = "https://n04.radiojar.com/78cxy6wkxtzuv"
	}
	if m.radioPlayer == "" {
		m.radioPlayer = detectRadioPlayer()
	}
	if m.radioPlayer == "" {
		m.radioErr = "no player found (install mpv or ffplay)"
		return
	}

	args := []string{}
	switch m.radioPlayer {
	case "mpv":
		args = []string{"--no-video", "--no-terminal", "--really-quiet", fmt.Sprintf("--volume=%d", m.radioVolume), m.radioURL}
	case "ffplay":
		args = []string{"-nodisp", "-loglevel", "quiet", "-volume", strconv.Itoa(m.radioVolume), m.radioURL}
	default:
		m.radioErr = "unsupported player: " + m.radioPlayer
		return
	}

	cmd := exec.Command(m.radioPlayer, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		m.radioErr = "radio start failed: " + err.Error()
		return
	}
	m.radioCmd = cmd
	m.radioPlaying = true
	m.radioLastPoll = time.Time{}
	go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
}

func (m *TUIModel) stopRadio() {
	if m.radioCmd != nil && m.radioCmd.Process != nil {
		_ = m.radioCmd.Process.Kill()
	}
	m.radioCmd = nil
	m.radioPlaying = false
	m.radioWave = ""
}

func detectRadioPlayer() string {
	if _, err := exec.LookPath("mpv"); err == nil {
		return "mpv"
	}
	if _, err := exec.LookPath("ffplay"); err == nil {
		return "ffplay"
	}
	return ""
}

func fetchNowPlayingCmd(stationID string) tea.Cmd {
	if strings.TrimSpace(stationID) == "" {
		return nil
	}
	return func() tea.Msg {
		artist, title, err := fetchNowPlaying(stationID)
		if err != nil {
			return radioNowPlayingMsg{Err: "now-playing unavailable"}
		}
		return radioNowPlayingMsg{Artist: artist, Title: title}
	}
}

func (m *TUIModel) handleBuiltInCommand(input string) tea.Cmd {
	if strings.EqualFold(strings.TrimSpace(input), "download this song") {
		return m.startDownloadCmd()
	}
	return nil
}

func (m *TUIModel) startDownloadCmd() tea.Cmd {
	stationID := m.radioStationID()
	if stationID == "" {
		m.radioErr = "no radio station configured"
		return nil
	}
	m.statusMode = "downloading"
	m.statusLine = "fetching song info…"
	m.radioErr = ""

	return func() tea.Msg {
		artist, title, err := fetchNowPlaying(stationID)
		if err != nil {
			return downloadDoneMsg{err: "now-playing unavailable"}
		}
		song := strings.TrimSpace(artist + " - " + title)
		query := strings.TrimSpace(artist + " " + title + " audio")
		if query == "" {
			return downloadDoneMsg{err: "empty song metadata"}
		}
		if _, err := exec.LookPath("yt-dlp"); err != nil {
			return downloadDoneMsg{err: "yt-dlp not installed"}
		}
		dir := "/home/v/Music/favorites"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return downloadDoneMsg{err: "cannot create favorites folder"}
		}
		metaPath := filepath.Join(dir, "favorites.txt")
		if f, err := os.OpenFile(metaPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(time.Now().Format("2006-01-02 15:04:05") + " | " + song + "\n")
			_ = f.Close()
		}
		outTmpl := filepath.Join(dir, "%(title)s [%(id)s].%(ext)s")
		cmd := exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "-o", outTmpl, "ytsearch1:"+query)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return downloadDoneMsg{err: "yt-dlp error"}
		}
		return downloadDoneMsg{artist: artist, title: title}
	}
}

func (m *TUIModel) handleMouseClick(x, y int) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	// Input box occupies the last 3 rows (top border + content + bottom border).
	inputStartY := m.height - 3
	if y >= inputStartY {
		m.focus = focusInput
		m.input.Focus()
		return
	}
	if !m.showTrace {
		m.tryClickCopyTarget(y)
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	// Left pane outer width = leftW(inner) + 2 borders. Right pane starts at leftW+2+1.
	total := m.width - 5
	leftW := total * m.leftPanePct / 100
	if leftW < 38 {
		leftW = 38
	}
	leftOuterEnd := leftW + 1 // 0-indexed: left border at 0, content 1..leftW, right border at leftW+1
	if x <= leftOuterEnd {
		m.tryClickCopyTarget(y)
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	// Right half — trace vs status
	if !m.showStatus {
		m.focus = focusTrace
		m.input.Blur()
		return
	}
	// Row where status pane starts: panes begin at row 1, trace outer = traceH+2, status starts after.
	remaining := m.height - 1 - 3 // header=1, input=3, no extra separators
	rightBudget := remaining - 4
	traceH := rightBudget * m.tracePanePct / 100
	if traceH < 4 {
		traceH = 4
	}
	tracePaneEndY := 1 + traceH + 2 // panes start row + trace outer height
	if y < tracePaneEndY {
		m.focus = focusTrace
	} else {
		m.focus = focusStatus
	}
	m.input.Blur()
}

// tryClickCopyTarget checks if the click row matches a copy icon and copies if so.
// Transcript content starts at terminal row 2 (header=row0, pane_top_border=row1).
func (m *TUIModel) tryClickCopyTarget(termY int) {
	const contentStartRow = 2
	if termY < contentStartRow {
		return
	}
	contentLine := (termY - contentStartRow) + m.transcript.YOffset
	for _, ct := range m.copyTargets {
		if contentLine == ct.lineNo || contentLine == ct.lineNo+1 {
			if err := copyToClipboard(ct.content); err != nil {
				m.statusLine = "copy failed: " + err.Error()
				m.statusMode = "error"
			} else {
				m.statusLine = "copied to clipboard!"
			}
			return
		}
	}
}

func copyToClipboard(text string) error {
	cmds := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
	}
	for _, args := range cmds {
		if _, err := exec.LookPath(args[0]); err == nil {
			c := exec.Command(args[0], args[1:]...)
			c.Stdin = strings.NewReader(text)
			return c.Run()
		}
	}
	return fmt.Errorf("no clipboard tool found (install wl-copy, xclip, or xsel)")
}

func (m *TUIModel) radioStationID() string {
	u := strings.TrimSpace(strings.TrimSuffix(m.radioURL, "/"))
	if u == "" {
		return ""
	}
	i := strings.LastIndex(u, "/")
	if i < 0 || i+1 >= len(u) {
		return ""
	}
	return u[i+1:]
}

func fetchNowPlaying(stationID string) (string, string, error) {
	if stationID == "" {
		return "", "", fmt.Errorf("missing station id")
	}
	url := "https://proxy.radiojar.com/api/stations/" + stationID + "/now_playing/?callback=x"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	s := strings.TrimSpace(string(b))
	start := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if start < 0 || end <= start {
		return "", "", fmt.Errorf("unexpected payload")
	}
	var payload struct {
		Artist string `json:"artist"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(s[start+1:end]), &payload); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(payload.Artist), strings.TrimSpace(payload.Title), nil
}

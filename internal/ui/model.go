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

// TUIModel is the Bubble Tea application model for the agent harness.
type TUIModel struct {
	width, height int

	transcript viewport.Model
	traceView  viewport.Model
	input      textinput.Model

	transcriptBuf strings.Builder
	traceBuf      strings.Builder

	focus        focus
	showTrace    bool
	showStatus   bool
	pendConfirm  *confirmState
	statusMode   string
	statusLine   string
	statusTick   int
	runSummary   string
	leftPanePct  int
	tracePanePct int
	radioURL     string
	radioPlayer  string
	radioVolume  int
	radioPlaying bool
	radioWave    string
	radioErr     string
	radioStep    int
	radioCmd     *exec.Cmd
	radioArtist  string
	radioTitle   string
	radioLastPoll time.Time

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

func (m *TUIModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.WindowSize(),
		func() tea.Msg { return tea.ClearScreen() },
		radioTickCmd(),
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

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.stopRadio()
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace

		case "ctrl+s":
			m.showStatus = !m.showStatus
			if !m.showStatus && m.focus == focusStatus {
				m.focus = focusTrace
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
					if m.handleBuiltInCommand(val) {
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

	// Header bar
	header := tuiHeaderStyle.Render("v100") +
		tuiHeaderDimStyle.Render("  Tab:focus  Shift+Tab:back  Shift+Arrows:resize  Ctrl+T:trace  Ctrl+S:status  Ctrl+C:quit")

	// Input box
	inputSt := tuiInputStyle
	if m.focus == focusInput {
		inputSt = tuiInputActiveStyle
	}
	inputBox := inputSt.Width(m.width - 2).Render(m.input.View())

	inputHeight := 3
	headerHeight := 1
	remaining := m.height - headerHeight - inputHeight - 4
	if remaining < 4 {
		remaining = 4
	}

	if m.showTrace {
		leftW := (m.width - 3) * m.leftPanePct / 100
		if leftW < 40 {
			leftW = 40
		}
		rightW := m.width - leftW - 3
		if rightW < 26 {
			rightW = 26
			leftW = m.width - rightW - 3
		}

		leftSt := tuiPaneStyle
		rightSt := tuiPaneStyle
		if m.focus == focusTranscript {
			leftSt = tuiActivePaneStyle
		}
		if m.focus == focusTrace {
			rightSt = tuiActivePaneStyle
		}

		statusH := 0
		traceH := remaining
		if m.showStatus {
			traceH = (remaining - 1) * m.tracePanePct / 100
			if traceH < 6 {
				traceH = 6
			}
			statusH = remaining - traceH - 1
			if statusH < 4 {
				statusH = 4
				traceH = remaining - statusH - 1
			}
		}

		m.transcript.Width = leftW - 4
		m.transcript.Height = remaining - 2
		m.traceView.Width = rightW - 4
		m.traceView.Height = traceH - 2

		left := leftSt.Width(leftW).Height(remaining).Render(m.transcript.View())
		tracePane := rightSt.Width(rightW).Height(traceH).Render(
			tuiTraceLabelStyle.Render("trace") + "\n" + m.traceView.View(),
		)
		rightCol := tracePane
		if m.showStatus {
			statusSt := tuiPaneStyle
			if m.focus == focusStatus {
				statusSt = tuiActivePaneStyle
			}
			statusPane := statusSt.Width(rightW).Height(statusH).Render(m.statusView(rightW))
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
	m.transcript.Width = m.width - 4
	m.transcript.Height = remaining - 2
	pane := tSt.Width(m.width - 2).Height(remaining).Render(m.transcript.View())
	view := lipgloss.JoinVertical(lipgloss.Left, header, pane, inputBox)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
}

func (m *TUIModel) appendEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Format(time.TimeOnly))
	m.updateStatusFromEvent(ev)

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
		wrapped := m.wrapPlainForTranscript(p.Content)
		m.transcriptBuf.WriteString(fmt.Sprintf(
			"\n%s  %s  %s\n",
			ts, styleUser.Render("you"), wrapped,
		))

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.focus = focusTranscript
		m.input.Blur()
		if p.Text != "" {
			rendered := m.renderMarkdownForPane(p.Text)
			m.transcriptBuf.WriteString(fmt.Sprintf(
				"\n%s  %s\n%s\n",
				ts, styleAssistant.Render("v100"), rendered,
			))
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

	// Trace pane: compact, semantic event stream with per-tool cues.
	m.traceBuf.WriteString(m.renderTraceEvent(ev) + "\n")

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
		if m.showStatus {
			m.focus = focusStatus
		} else {
			m.focus = focusInput
			m.input.Focus()
		}
	case focusStatus:
		m.focus = focusInput
		m.input.Focus()
	}
}

func (m *TUIModel) cycleFocusBack() {
	switch m.focus {
	case focusInput:
		if m.showStatus {
			m.focus = focusStatus
		} else if m.showTrace {
			m.focus = focusTrace
		} else {
			m.focus = focusTranscript
		}
		m.input.Blur()
	case focusTranscript:
		m.focus = focusInput
		m.input.Focus()
	case focusTrace:
		m.focus = focusTranscript
		m.input.Blur()
	case focusStatus:
		m.focus = focusTrace
		m.input.Blur()
	}
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
	m.transcriptBuf.WriteString(styleMuted.Render("Enter") + " send  " + styleMuted.Render("Tab") + " focus  " + styleMuted.Render("Ctrl+T") + " trace  " + styleMuted.Render("Ctrl+S") + " status  " + styleMuted.Render("Ctrl+C") + " quit\n\n")

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

func (m *TUIModel) statusView(width int) string {
	line := m.statusLine
	w := width - 6
	if w < 12 {
		w = 12
	}
	line = wrap.String(line, w)

	radio := styleMuted.Render("radio") + " " + m.radioStateLine()
	radio += "\n" + styleMuted.Render("feed: "+m.radioURL)
	if m.radioArtist != "" || m.radioTitle != "" {
		radio += "\n" + stylePrimary.Render("now: "+strings.TrimSpace(m.radioArtist+" - "+m.radioTitle))
	}
	if m.radioWave != "" {
		wave := m.renderWaveForWidth(w)
		radio += "\n" + styleInfo.Copy().Width(w).Align(lipgloss.Center).Render(wave)
	}
	if m.radioErr != "" {
		radio += "\n" + styleFail.Render(m.radioErr)
	}
	return tuiStatusLabelStyle.Render("status") + "\n" +
		stylePrimary.Render(wrap.String(m.runSummary, w)) + "\n" +
		styleBold.Render(strings.ToUpper(m.statusMode)) + "\n" +
		styleMuted.Render(line) + "\n\n" +
		radio
}

func (m *TUIModel) renderTraceEvent(ev core.Event) string {
	t := styleMuted.Render(ev.TS.Format(time.TimeOnly))
	switch ev.Type {
	case core.EventRunStart:
		return t + "  " + styleInfo.Render("🚀 run.start")
	case core.EventUserMsg:
		return t + "  " + styleUser.Render("🧑 user.message")
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if len(p.ToolCalls) > 0 {
			return t + "  " + styleAssistant.Render(fmt.Sprintf("🧠 model.response (%d tool calls)", len(p.ToolCalls)))
		}
		return t + "  " + styleAssistant.Render("🧠 model.response")
	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return t + "  " + styleWarn.Render(fmt.Sprintf("%s tool.call %s", toolEmoji(p.Name), p.Name))
	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.OK {
			return t + "  " + styleOK.Render(fmt.Sprintf("✅ tool.result %s", p.Name))
		}
		return t + "  " + styleFail.Render(fmt.Sprintf("❌ tool.result %s", p.Name))
	case core.EventRunError:
		return t + "  " + styleFail.Render("💥 run.error")
	case core.EventRunEnd:
		return t + "  " + styleInfo.Render("🏁 run.end")
	default:
		return t + "  " + styleRunID.Render(string(ev.Type))
	}
}

func toolEmoji(name string) string {
	switch name {
	case "fs_list":
		return "📁"
	case "fs_read":
		return "📖"
	case "fs_write":
		return "📝"
	case "fs_mkdir":
		return "📂"
	case "project_search":
		return "🔎"
	case "patch_apply":
		return "🩹"
	case "git_status":
		return "🌿"
	case "git_diff":
		return "🧾"
	case "git_commit":
		return "✅"
	case "sh":
		return "🖥"
	default:
		return "⚙"
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

func (m *TUIModel) handleBuiltInCommand(input string) bool {
	if strings.EqualFold(strings.TrimSpace(input), "download this song") {
		m.downloadCurrentSong()
		return true
	}
	return false
}

func (m *TUIModel) downloadCurrentSong() {
	artist, title, err := fetchNowPlaying(m.radioStationID())
	if err != nil {
		m.radioErr = "download failed: now-playing unavailable"
		m.statusMode = "error"
		m.statusLine = "could not fetch current song"
		return
	}
	m.radioArtist = artist
	m.radioTitle = title

	query := strings.TrimSpace(artist + " " + title + " audio")
	if query == "" {
		m.radioErr = "download failed: empty song metadata"
		m.statusMode = "error"
		m.statusLine = "current song has no artist/title"
		return
	}

	if _, err := exec.LookPath("yt-dlp"); err != nil {
		m.radioErr = "download failed: yt-dlp not installed"
		m.statusMode = "error"
		m.statusLine = "install yt-dlp to enable song download"
		return
	}

	dir := "/home/v/Music/favorites"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.radioErr = "download failed: cannot create favorites folder"
		m.statusMode = "error"
		m.statusLine = "failed to create /home/v/Music/favorites"
		return
	}

	metaPath := filepath.Join(dir, "favorites.txt")
	f, err := os.OpenFile(metaPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.WriteString(time.Now().Format("2006-01-02 15:04:05") + " | " + strings.TrimSpace(artist+" - "+title) + "\n")
		_ = f.Close()
	}

	outTmpl := filepath.Join(dir, "%(title)s [%(id)s].%(ext)s")
	cmd := exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "-o", outTmpl, "ytsearch1:"+query)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		m.radioErr = "download failed: yt-dlp error"
		m.statusMode = "error"
		m.statusLine = "could not download: " + strings.TrimSpace(artist+" - "+title)
		return
	}

	m.radioErr = ""
	m.statusMode = "idle"
	m.statusLine = "downloaded: " + strings.TrimSpace(artist+" - "+title)
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

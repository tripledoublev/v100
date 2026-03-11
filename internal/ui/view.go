package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

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

	if m.showRadioSelect {
		return m.radioSelectView()
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
		metricsH := 9 // fixed height for visual inspector (7 content lines + 2 border rows)
		var traceH int

		// lipgloss .Height(h) sets total rendered height including borders.
		// Left column height = paneInnerH + 2 = remaining.
		// Right column total height must also = remaining.
		// Right Height = hTrace + hMetrics + hStatus (if shown)
		if m.showStatus {
			// Total height budget for 3 panes must sum to 'remaining'.
			budget := remaining - metricsH
			statusH = 12 // fixed height for status
			traceH = budget - statusH
			if traceH < 4 {
				traceH = 4
				statusH = budget - traceH
			}
		} else {
			// Budget for 2 panes
			traceH = remaining - metricsH
			if traceH < 4 {
				traceH = 4
			}
		}

		m.transcript.Width = leftW - 4
		m.transcript.Height = paneInnerH
		
		// Trace viewport height: traceH total - 2 (borders) - 1 (label) = traceH - 3
		m.traceView.Width = rightW - 4
		m.traceView.Height = traceH - 3
		if m.traceView.Height < 1 {
			m.traceView.Height = 1
		}

		left := leftSt.Width(leftW).Height(paneInnerH).Render(m.transcript.View())
		
		// 1. Trace Pane
		tracePane := rightSt.Width(rightW).Height(traceH).Render(
			tuiTraceLabelStyle.Render("trace") + "\n" + m.traceView.View(),
		)
		
		// 2. Visual Inspector (Fixed height)
		metricsView := LiveMetricDashboard(m.currentStep, m.maxSteps, m.usedTokens, m.maxTokens, m.inputTokens, m.outputTokens, m.usedCost, m.maxCost, rightW)
		metricsPane := tuiPaneStyle.Width(rightW).Height(metricsH).Render(metricsView)

		// 3. Status Pane (if shown)
		var rightCol string
		if m.showStatus {
			statusSt := tuiPaneStyle
			if m.focus == focusStatus {
				statusSt = tuiActivePaneStyle
			}
			statusPane := statusSt.Width(rightW).Height(statusH).Render(m.statusView(rightW, statusH))
			rightCol = lipgloss.JoinVertical(lipgloss.Left, tracePane, metricsPane, statusPane)
		} else {
			rightCol = lipgloss.JoinVertical(lipgloss.Left, tracePane, metricsPane)
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

func (m *TUIModel) statusView(width, height int) string {
	w := width - 2 // content width inside borders
	if w < 12 {
		w = 12
	}

	lines := []string{
		tuiStatusLabelStyle.Render("status"),
		stylePrimary.Render(wrap.String(m.runSummary, w)),
		styleBold.Render(strings.ToUpper(m.statusMode)),
		styleMuted.Render(wrap.String(m.statusLine, w)),
		"",
		styleMuted.Render(fmt.Sprintf("sub-agents: active=%d done=%d failed=%d",
			len(m.activeAgents), m.agentDoneCount, m.agentFailCount)),
		styleMuted.Render(m.subAgentStatusLine()),
		"",
		styleMuted.Render("radio") + " " + m.radioStateLine(),
		styleMuted.Render(wrap.String("feed: "+m.radioURL, w)),
	}
	if m.radioArtist != "" || m.radioTitle != "" {
		lines = append(lines, stylePrimary.Render(wrap.String("now: "+strings.TrimSpace(m.radioArtist+" - "+m.radioTitle), w)))
	}
	if m.radioWave != "" {
		wave := m.renderWaveForWidth(w)
		lines = append(lines, styleInfo.Render(centerToWidth(wave, w)))
	}
	if m.radioErr != "" {
		lines = append(lines, styleFail.Render(wrap.String(m.radioErr, w)))
	}

	// Flatten wrapped lines into a single list of strings
	var flattened []string
	for _, l := range lines {
		parts := strings.Split(l, "\n")
		flattened = append(flattened, parts...)
	}

	// Keep content bounded to pane height to avoid stale lines after resize.
	contentH := height - 2 // border consumes 2 lines
	if contentH < 1 {
		contentH = 1
	}
	if len(flattened) > contentH {
		flattened = flattened[:contentH]
	}
	return strings.Join(flattened, "\n")
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

func (m *TUIModel) seedWelcomeContent() {
	now := time.Now().Format("2006-01-02 15:04:05")

	m.transcriptBuf.WriteString(stylePrimary.Render("control deck") + styleMuted.Render(" • session ready • "+now) + "\n\n")

	m.transcriptBuf.WriteString(styleBold.Render("Controls") + "\n")
	m.transcriptBuf.WriteString(styleMuted.Render("Enter") + " send  " + styleMuted.Render("Tab") + " focus  " + styleMuted.Render("Ctrl+Shift+Tab") + " half  " + styleMuted.Render("Ctrl+T") + " trace  " + styleMuted.Render("Ctrl+S") + " status  " + styleMuted.Render("Ctrl+C") + " quit\n\n")

	m.transcriptBuf.WriteString(styleMuted.Render("Type a task below and press Enter."))

	m.traceBuf.WriteString(tuiTraceLabelStyle.Render("trace stream") + "\n")
	m.traceBuf.WriteString(styleMuted.Render("waiting for events...") + "\n\n")
	m.traceBuf.WriteString(styleMuted.Render("run_start  model response  tool_call  tool_result  run_end"))

	m.transcript.SetContent(m.transcriptBuf.String())
	m.traceView.SetContent(m.traceBuf.String())
}

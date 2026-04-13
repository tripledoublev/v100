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

	// Input box
	inputSt := tuiInputStyle
	if m.focus == focusInput {
		inputSt = tuiInputActiveStyle
	}
	inputView := m.input.View()
	if len(m.pastedImages) > 0 {
		inputView = styleInfo.Render(imageCount(len(m.pastedImages))) + " " + inputView
	}
	inputBox := inputSt.Width(m.width - singleBorderSize).Render(inputView)
	inputHeight := lipgloss.Height(inputBox)
	layoutPlan := computeViewLayout(m.width, m.height, inputHeight, 1, m.leftPanePct, m.tracePanePct, m.showTrace, time.Now())
	header := renderHeader(m.width, layoutPlan.header)
	headerHeight := lipgloss.Height(header)
	if headerHeight != 1 {
		layoutPlan = computeViewLayout(m.width, m.height, inputHeight, headerHeight, m.leftPanePct, m.tracePanePct, m.showTrace, time.Now())
		header = renderHeader(m.width, layoutPlan.header)
	}
	layout := layoutPlan.panes

	transcript := TranscriptPanel{m: m, viewportHeight: layout.transcriptHeight}

	if m.showTrace {
		trace := TracePanel{m}
		metrics := MetricsPanel{m}
		status := StatusPanel{m: m, contentHeight: layout.maxStatusContentHeight}

		// ── Height allocation ──────────────────────────────────────
		// Render fixed-size panels first, measure their rendered height,
		// then give the scrollable trace pane whatever is left.

		var metricsPane string
		var metricsRendered int
		if m.showMetrics {
			metricsPane = m.renderPanel(metrics, layout.rightWidth, 0)
			metricsRendered = lipgloss.Height(metricsPane)
		}

		var statusPane string
		var statusRendered int
		if m.showStatus {
			statusPane = m.renderPanel(status, layout.rightWidth, 0)
			statusRendered = lipgloss.Height(statusPane)
		}

		layout = layout.withRightColumnHeights(metricsRendered, statusRendered)

		left := m.renderPanel(transcript, layout.leftWidth, layoutPlan.leftPaneHeight)
		tracePane := m.renderPanel(trace, layout.rightWidth, layout.traceContentHeight)

		rightPanes := []string{tracePane}
		if m.showMetrics {
			rightPanes = append(rightPanes, metricsPane)
		}
		if m.showStatus {
			rightPanes = append(rightPanes, statusPane)
		}
		rightCol := lipgloss.JoinVertical(lipgloss.Left, rightPanes...)

		panes := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", rightCol)
		view := lipgloss.JoinVertical(lipgloss.Left, header, panes, inputBox)
		return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
	}

	// Single pane
	transcript.viewportHeight = layoutPlan.singlePaneHeight
	pane := m.renderPanel(transcript, layoutPlan.inputWidth, layoutPlan.singlePaneHeight)
	view := lipgloss.JoinVertical(lipgloss.Left, header, pane, inputBox)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
}

func truncateHeaderText(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= maxWidth {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *TUIModel) statusView(width, contentHeight int) string {
	w := width - singleBorderSize // content width inside borders
	if w < 12 {
		w = 12
	}

	lines := []string{
		tuiStatusLabelStyle.Render("status"),
		stylePrimary.Render(wrap.String(m.runSummary, w)),
		styleBold.Render(strings.ToUpper(m.statusMode)),
		styleMuted.Render(wrap.String(m.statusLine, w)),
		styleMuted.Render(m.deviceStatusLine()),
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
	// contentHeight is already pre-computed (without border overhead)
	if contentHeight < 1 {
		contentHeight = 1
	}
	if len(flattened) > contentHeight {
		flattened = flattened[:contentHeight]
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

	width := m.transcriptWrapWidth()
	if width < 24 {
		width = 24
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

func (m *TUIModel) wrapTranscriptBlock(text, indent string) string {
	w := m.transcriptWrapWidth() - lipgloss.Width(indent)
	if w < 20 {
		w = 20
	}
	wrapped := wrap.String(text, w)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func (m *TUIModel) transcriptWrapWidth() int {
	if m.width <= 0 {
		return 80
	}
	if m.showTrace {
		layout := computePaneLayout(m.width, max(4, m.height), m.leftPanePct, m.tracePanePct)
		return max(1, layout.transcriptWidth)
	}
	return max(1, m.width-4)
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

	m.addItem(&TranscriptItem{
		Type:      ItemWelcome,
		Text:      now,
		Timestamp: time.Now(),
	})

	m.traceBuf.WriteString(tuiTraceLabelStyle.Render("trace stream") + "\n")
	m.traceBuf.WriteString(styleMuted.Render("waiting for events...") + "\n\n")
	m.traceBuf.WriteString(styleMuted.Render("run_start  model response  tool_call  tool_result  run_end"))

	m.rebuildTranscript(true)
	m.traceView.SetContent(m.traceBuf.String())
}

package ui

import "time"

// Panel is the rendering contract for a TUI section.
// Panels are render-only views over TUIModel state — they hold no mutable
// state of their own and are instantiated fresh on each View() call.
type Panel interface {
	// Render returns styled content for the given dimensions.
	// width and height are content dimensions (inside borders).
	Render(width, height int) string
	// Focusable reports whether this panel can receive focus.
	Focusable() bool
	// FocusID returns the focus constant this panel corresponds to.
	FocusID() focus
}

// renderPanel wraps a Panel's output in a bordered pane style, using the
// active style when the panel has focus. When height <= 0 the pane renders
// at natural size (needed for measurement before layout adjustment).
func (m *TUIModel) renderPanel(p Panel, width, height int) string {
	st := tuiPaneStyle
	if p.Focusable() && p.FocusID() == m.focus {
		st = tuiActivePaneStyle
	}
	st = st.Width(width)
	if height > 0 {
		st = st.Height(height)
	}
	return st.Render(p.Render(width, height))
}

// ── TranscriptPanel ──────────────────────────────────────────────────────────

// TranscriptPanel renders the main scrollable transcript viewport.
type TranscriptPanel struct {
	m              *TUIModel
	viewportHeight int
}

func (p TranscriptPanel) Render(width, height int) string {
	// In split mode the pane border + viewport margin needs ~4 cols;
	// in single mode the caller already subtracted border overhead.
	vw := width
	if p.m.showTrace {
		vw = max(1, width-4)
	}
	p.m.transcript.Width = vw
	vh := height
	if p.viewportHeight > 0 {
		vh = p.viewportHeight
	}
	p.m.transcript.Height = max(1, vh)
	return p.m.transcript.View()
}

func (TranscriptPanel) Focusable() bool { return true }
func (TranscriptPanel) FocusID() focus  { return focusTranscript }

// ── TracePanel ───────────────────────────────────────────────────────────────

// TracePanel renders the event trace viewport with a label.
type TracePanel struct{ m *TUIModel }

func (p TracePanel) Render(width, height int) string {
	p.m.traceView.Width = max(1, width-4)
	p.m.traceView.Height = max(1, height-1) // -1 for label line
	return tuiTraceLabelStyle.Render("trace") + "\n" + p.m.traceView.View()
}

func (TracePanel) Focusable() bool { return true }
func (TracePanel) FocusID() focus  { return focusTrace }

// ── MetricsPanel ─────────────────────────────────────────────────────────────

// MetricsPanel renders the visual inspector / metrics dashboard.
type MetricsPanel struct{ m *TUIModel }

func (p MetricsPanel) Render(width, _ int) string {
	return LiveMetricDashboard(
		p.m.currentStep, p.m.maxSteps,
		p.m.usedTokens, p.m.maxTokens, p.m.inputTokens, p.m.outputTokens,
		p.m.usedCost, p.m.maxCost, p.m.lastStepMS, p.m.lastStepTools,
		len(p.m.modelEvents), len(p.m.toolEvents), len(p.m.compressEvents),
		p.m.statusMode, time.Since(p.m.lastEventAt), width)
}

func (MetricsPanel) Focusable() bool { return false }
func (MetricsPanel) FocusID() focus  { return -1 }

// ── StatusPanel ──────────────────────────────────────────────────────────────

// StatusPanel renders the status/radio/agent information pane.
type StatusPanel struct {
	m             *TUIModel
	contentHeight int
}

func (p StatusPanel) Render(width, height int) string {
	contentHeight := height
	if p.contentHeight > 0 {
		contentHeight = p.contentHeight
	}
	return p.m.statusView(width, contentHeight)
}

func (StatusPanel) Focusable() bool { return true }
func (StatusPanel) FocusID() focus  { return focusStatus }

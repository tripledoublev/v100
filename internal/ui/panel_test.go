package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	lipgloss "github.com/charmbracelet/lipgloss"
)

func newTestModel() *TUIModel {
	m := &TUIModel{
		width:       120,
		height:      40,
		showTrace:   true,
		showMetrics: true,
		showStatus:  true,
		statusMode:  "idle",
		statusLine:  "ready",
		runSummary:  "test run",
		leftPanePct: 55,
		transcript:  viewport.New(60, 20),
		traceView:   viewport.New(40, 15),
	}
	m.transcript.SetContent("hello world")
	m.traceView.SetContent("trace event")
	return m
}

func TestTranscriptPanel_Contract(t *testing.T) {
	p := TranscriptPanel{m: newTestModel(), viewportHeight: 20}
	if !p.Focusable() {
		t.Error("TranscriptPanel should be focusable")
	}
	if p.FocusID() != focusTranscript {
		t.Errorf("FocusID = %d, want %d", p.FocusID(), focusTranscript)
	}
	out := p.Render(60, 20)
	if out == "" {
		t.Error("Render returned empty string")
	}
}

func TestTracePanel_Contract(t *testing.T) {
	p := TracePanel{newTestModel()}
	if !p.Focusable() {
		t.Error("TracePanel should be focusable")
	}
	if p.FocusID() != focusTrace {
		t.Errorf("FocusID = %d, want %d", p.FocusID(), focusTrace)
	}
	out := p.Render(40, 15)
	if !strings.Contains(out, "trace") {
		t.Error("Render should contain trace label")
	}
}

func TestMetricsPanel_Contract(t *testing.T) {
	p := MetricsPanel{newTestModel()}
	if p.Focusable() {
		t.Error("MetricsPanel should not be focusable")
	}
	out := p.Render(40, 0)
	if out == "" {
		t.Error("Render returned empty string")
	}
}

func TestStatusPanel_Contract(t *testing.T) {
	p := StatusPanel{m: newTestModel(), contentHeight: 10}
	if !p.Focusable() {
		t.Error("StatusPanel should be focusable")
	}
	if p.FocusID() != focusStatus {
		t.Errorf("FocusID = %d, want %d", p.FocusID(), focusStatus)
	}
	out := p.Render(40, 10)
	if out == "" {
		t.Error("Render returned empty string")
	}
}

func TestRenderPanel_FocusRouting(t *testing.T) {
	m := newTestModel()

	// Verify renderPanel does not panic for each focus state
	for _, f := range []focus{focusInput, focusTranscript, focusTrace, focusStatus} {
		m.focus = f
		out := m.renderPanel(TranscriptPanel{m: m, viewportHeight: 20}, 60, 20)
		if out == "" {
			t.Errorf("renderPanel returned empty for focus=%d", f)
		}
	}
}

func TestRenderPanel_NaturalHeight(t *testing.T) {
	m := newTestModel()
	m.maxSteps = 10
	m.currentStep = 3

	// height=0 should render at natural size without crashing
	out := m.renderPanel(MetricsPanel{m}, 40, 0)
	if out == "" {
		t.Error("natural height render returned empty")
	}
}

func TestTranscriptPanel_UsesViewportHeightOverride(t *testing.T) {
	m := newTestModel()
	p := TranscriptPanel{m: m, viewportHeight: 3}

	_ = p.Render(60, 1)
	if m.transcript.Height != 3 {
		t.Fatalf("transcript height = %d, want 3", m.transcript.Height)
	}
}

func TestStatusPanel_NaturalHeightStaysCompact(t *testing.T) {
	m := newTestModel()
	m.runSummary = "short"
	m.statusLine = "short"
	m.radioURL = ""
	m.showStatus = true

	panel := StatusPanel{m: m, contentHeight: 30}
	natural := lipgloss.Height(m.renderPanel(panel, 40, 0))
	fixed := lipgloss.Height(m.renderPanel(panel, 40, 30))
	if natural >= fixed {
		t.Fatalf("natural status height = %d, want less than fixed height %d", natural, fixed)
	}
}

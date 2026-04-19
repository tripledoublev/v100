package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
)

func updateKey(m *TUIModel, msg tea.KeyMsg) *TUIModel {
	updated, _ := m.Update(msg)
	return updated.(*TUIModel)
}

func TestViewRendersHeaderInBoundedHeight(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 140
	m.height = 42
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("expected non-empty view")
	}
	first := stripANSI(lines[0])
	if !strings.Contains(first, "v100") {
		t.Fatalf("expected header on first line, got first line: %q", first)
	}
}

func TestNewTUIModelEnablesVisualInspectorByDefault(t *testing.T) {
	m := NewTUIModel(false, false)
	if !m.showMetrics {
		t.Fatal("expected visual inspector to be enabled by default")
	}

	m.width = 120
	m.height = 30
	view := stripANSI(m.View())
	if !strings.Contains(view, "visual inspector") {
		t.Fatalf("expected default view to include visual inspector, got:\n%s", view)
	}
}

func TestViewKeepsClockVisibleInHeader(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 100
	m.height = 30

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("expected non-empty view")
	}
	first := stripANSI(lines[0])
	if !strings.Contains(first, "v100") {
		t.Fatalf("expected header on first line, got %q", first)
	}
	matched, err := regexp.MatchString(`\b\d{2}:\d{2}:\d{2}\b`, first)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatalf("expected visible clock in header line, got %q", first)
	}
}

func TestToolResultRendersAsIndentedBlock(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 80
	m.height = 24

	callID := "test-call-id"
	callPayload, _ := json.Marshal(core.ToolCallPayload{
		CallID: callID,
		Name:   "sh",
		Args:   "ls -la",
	})
	m.appendEvent(core.Event{
		TS:      time.Now(),
		Type:    core.EventToolCall,
		Payload: callPayload,
	})

	payload, err := json.Marshal(core.ToolResultPayload{
		CallID:     callID,
		Name:       "sh",
		OK:         true,
		DurationMS: 123,
		Output:     "first useful line with a lot of detail that should wrap cleanly in the transcript pane rather than making one awkward overstuffed row",
	})
	if err != nil {
		t.Fatal(err)
	}

	m.appendEvent(core.Event{
		TS:      time.Now(),
		Type:    core.EventToolResult,
		Payload: payload,
	})

	// Expand the tool group so details are rendered
	for _, item := range m.history {
		if item.Type == ItemToolGroup {
			item.Expanded = true
		}
	}
	m.rebuildTranscript(true)

	out := stripANSI(m.transcriptBuf.String())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line tool result block, got %q", out)
	}
	idx := -1
	for i, line := range lines {
		if strings.Contains(line, "sh") && strings.Contains(line, "[123ms]") {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("expected tool metadata line in transcript, got %q", out)
	}
	if idx+1 >= len(lines) {
		t.Fatalf("expected wrapped tool summary after metadata line, got %q", out)
	}
	if !strings.HasPrefix(lines[idx+1], "             ") {
		t.Fatalf("expected indented wrapped tool summary, got %q", lines[idx+1])
	}
}

func TestModelResponseDoesNotStealInputFocus(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 100
	m.height = 30
	m.focus = focusInput
	m.input.Focus()
	m.input.SetValue("draft prompt")

	payload, err := json.Marshal(core.ModelRespPayload{
		Text: "response text",
	})
	if err != nil {
		t.Fatal(err)
	}

	m.appendEvent(core.Event{
		TS:      time.Now(),
		Type:    core.EventModelResp,
		Payload: payload,
	})

	if m.focus != focusInput {
		t.Fatalf("focus = %v, want input focus preserved", m.focus)
	}
	if m.input.Value() != "draft prompt" {
		t.Fatalf("input value = %q, want preserved draft", m.input.Value())
	}
}

func TestUserMessageAppearsInTranscript(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 100
	m.height = 30

	payload, err := json.Marshal(core.UserMsgPayload{
		Content: "nice!",
	})
	if err != nil {
		t.Fatal(err)
	}

	m.appendEvent(core.Event{
		TS:      time.Date(2026, 3, 18, 18, 28, 36, 0, time.UTC),
		Type:    core.EventUserMsg,
		Payload: payload,
	})

	out := stripANSI(m.transcriptBuf.String())
	if !strings.Contains(out, "nice!") || !strings.Contains(out, userMessageLabel) {
		t.Fatalf("expected user message content in transcript, got %q", out)
	}
	wantTS := time.Date(2026, 3, 18, 18, 28, 36, 0, time.UTC).Local().Format(time.TimeOnly)
	if !strings.Contains(out, wantTS) {
		t.Fatalf("expected timestamp to remain visible, got %q", out)
	}
}

func TestTranscriptWrapWidthUsesComputedPaneWidth(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 120
	m.height = 30
	m.showTrace = true
	m.leftPanePct = 50
	m.tracePanePct = 50

	layout := computePaneLayout(m.width, m.height, m.leftPanePct, m.tracePanePct)
	if got, want := m.transcriptWrapWidth(), layout.transcriptWidth; got != want {
		t.Fatalf("transcriptWrapWidth() = %d, want %d", got, want)
	}
}

func TestViewResponsiveLayoutsPreservePanelsAndWidthBounds(t *testing.T) {
	widths := []int{92, 120, 160}
	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := NewTUIModel(false, false)
			m.width = width
			m.height = 30
			m.showTrace = true
			m.showStatus = true
			m.showMetrics = true
			m.lastEventAt = time.Now().Add(-3 * time.Second)
			m.runSummary = "A long status summary that should wrap instead of overflowing the pane width while the visual inspector, trace, and status panels remain visible."
			m.statusLine = "The harness is actively rendering multiple panes and this line should stay within the available width after wrapping."

			view := stripANSI(m.View())
			assertViewWithinWidth(t, view, width)
			for _, want := range []string{"trace", "status", "visual inspector"} {
				if !strings.Contains(view, want) {
					t.Fatalf("expected %q in responsive layout at width %d", want, width)
				}
			}
		})
	}
}

func TestCtrlDOpensAndFocusesDetailPane(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 140
	m.height = 40
	m.focus = focusTranscript
	m.input.Blur()
	m.selectedToolExec = &ToolExecution{Name: "sh", Result: "done"}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlD})

	if !m.showDetail {
		t.Fatal("expected detail pane to open")
	}
	if m.focus != focusDetail {
		t.Fatalf("focus = %v, want detail", m.focus)
	}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.showDetail {
		t.Fatal("expected detail pane to close")
	}
	if m.focus != focusTranscript {
		t.Fatalf("focus = %v, want transcript after closing detail", m.focus)
	}
}

func TestCycleFocusIncludesDetailPaneWhenVisible(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 140
	m.height = 40
	m.showTrace = true
	m.showStatus = true
	m.showDetail = true
	m.selectedToolExec = &ToolExecution{Name: "sh", Result: "done"}
	m.focus = focusTranscript

	m.cycleFocus()
	if m.focus != focusDetail {
		t.Fatalf("focus after transcript = %v, want detail", m.focus)
	}
	m.cycleFocus()
	if m.focus != focusTrace {
		t.Fatalf("focus after detail = %v, want trace", m.focus)
	}
	m.cycleFocus()
	if m.focus != focusStatus {
		t.Fatalf("focus after trace = %v, want status", m.focus)
	}
	m.cycleFocus()
	if m.focus != focusInput {
		t.Fatalf("focus after status = %v, want input", m.focus)
	}
	m.cycleFocus()
	if m.focus != focusTranscript {
		t.Fatalf("focus after input = %v, want transcript", m.focus)
	}
}

func TestMouseClickInDetailColumnFocusesDetailPane(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 150
	m.height = 40
	m.showTrace = true
	m.showStatus = true
	m.showDetail = true
	m.selectedToolExec = &ToolExecution{Name: "sh", Result: "done"}
	m.focus = focusTranscript

	transcriptEnd, detailEnd := m.threeColumnBoundaries()
	clickX := transcriptEnd + 2
	if clickX > detailEnd {
		t.Fatalf("computed click x=%d outside detail bounds ending at %d", clickX, detailEnd)
	}

	m.handleMouseClick(clickX, 5)

	if m.focus != focusDetail {
		t.Fatalf("focus = %v, want detail", m.focus)
	}
}

func TestClickToolDetailOpensAndFocusesDetailPane(t *testing.T) {
	m := NewTUIModel(false, false)
	m.detailTargets = []toolDetailTarget{{
		lineNo: 0,
		exec:   &ToolExecution{Name: "sh", Result: "done"},
	}}

	m.tryClickToolDetail(2)

	if !m.showDetail {
		t.Fatal("expected detail pane to open")
	}
	if m.focus != focusDetail {
		t.Fatalf("focus = %v, want detail", m.focus)
	}
	if m.selectedToolExec == nil || m.selectedToolExec.Name != "sh" {
		t.Fatalf("selected tool = %#v, want sh execution", m.selectedToolExec)
	}
}

func TestViewResizeKeepsPanelsVisibleAndBounded(t *testing.T) {
	m := NewTUIModel(false, false)
	m.showTrace = true
	m.showStatus = true
	m.showMetrics = true
	m.lastEventAt = time.Now().Add(-2 * time.Second)
	m.runSummary = "Resize regression coverage for transcript, inspector, and status composition."
	m.statusLine = "This content should remain visible and wrapped after resizing the terminal."

	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 150, height: 36},
		{width: 96, height: 24},
	} {
		m.width = size.width
		m.height = size.height

		view := stripANSI(m.View())
		assertViewWithinWidth(t, view, size.width)
		for _, want := range []string{"trace", "status", "visual inspector"} {
			if !strings.Contains(view, want) {
				t.Fatalf("expected %q after resize to %dx%d", want, size.width, size.height)
			}
		}
	}
}

func TestViewSinglePaneOmitsRightColumnPanels(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 100
	m.height = 26
	m.showTrace = false
	m.showStatus = true
	m.showMetrics = true
	m.transcript.SetContent("single pane transcript")
	m.traceView.SetContent("right pane trace body")
	m.runSummary = "right pane inspector"
	m.statusLine = "right pane status"

	view := stripANSI(m.View())
	assertViewWithinWidth(t, view, m.width)
	for _, unwanted := range []string{"visual inspector", "right pane trace body", "right pane inspector", "right pane status"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("did not expect %q in single-pane layout", unwanted)
		}
	}
	if !strings.Contains(view, "single pane transcript") {
		t.Fatal("expected transcript content in single-pane layout")
	}
}

func assertViewWithinWidth(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if line == "" {
			continue
		}
		if lipgloss.Width(line) > width {
			t.Fatalf("line exceeds width %d: %q", width, line)
		}
	}
}

func TestFocusCycleLeftHalf(t *testing.T) {

	m := NewTUIModel(false, false)
	m.focus = focusTranscript

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusInput {
		t.Fatalf("expected focusInput after tab, got %v", m.focus)
	}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript after second tab, got %v", m.focus)
	}
}

func TestFocusHalfSwitchAndRightHalfCycling(t *testing.T) {
	m := NewTUIModel(false, false)
	m.showTrace = true
	m.showStatus = true
	m.focus = focusTranscript

	// Switch to right half with a robust fallback key.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if m.focus != focusTrace {
		t.Fatalf("expected focusTrace after half-switch, got %v", m.focus)
	}

	// Tab cycles within right half (trace <-> status).
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusStatus {
		t.Fatalf("expected focusStatus after tab in right half, got %v", m.focus)
	}

	m = updateKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != focusTrace {
		t.Fatalf("expected focusTrace after shift+tab in right half, got %v", m.focus)
	}

	// Half-switch back to left.
	m = updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.focus != focusTranscript {
		t.Fatalf("expected focusTranscript after switching back, got %v", m.focus)
	}
}

func TestEnterSubmitsAttachedImages(t *testing.T) {
	m := NewTUIModel(false, false)
	m.pastedImages = [][]byte{{0x89, 0x50, 0x4e, 0x47}}
	got := make(chan SubmitRequest, 1)
	m.SubmitFn = func(req SubmitRequest) {
		got <- req
	}

	updateKey(m, tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case req := <-got:
		if req.Text != "" {
			t.Fatalf("expected empty text, got %q", req.Text)
		}
		if len(req.Images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(req.Images))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submit")
	}

	if len(m.pastedImages) != 0 {
		t.Fatalf("expected pasted images cleared after submit, got %d", len(m.pastedImages))
	}
}

func TestCtrlVPastesClipboardImage(t *testing.T) {
	m := NewTUIModel(false, false)
	prev := clipboardImageReader
	clipboardImageReader = func() ([]byte, error) {
		return []byte{0x89, 0x50, 0x4e, 0x47}, nil
	}
	defer func() { clipboardImageReader = prev }()

	updateKey(m, tea.KeyMsg{Type: tea.KeyCtrlV})

	if len(m.pastedImages) != 1 {
		t.Fatalf("expected 1 pasted image, got %d", len(m.pastedImages))
	}
	if !strings.Contains(m.statusLine, "Image #1") {
		t.Fatalf("expected attachment status, got %q", m.statusLine)
	}
}

func TestStatusModeDisplay_Stalled(t *testing.T) {
	m := NewTUIModel(false, false)
	m.statusMode = "thinking"

	// Set lastEventAt to 11 seconds ago
	m.lastEventAt = time.Now().Add(-11 * time.Second)

	out := m.statusModeDisplay()
	if !strings.Contains(out, "STALLED") {
		t.Errorf("expected STALLED in status display, got %q", out)
	}

	// Reset lastEventAt to now
	m.lastEventAt = time.Now()
	out = m.statusModeDisplay()
	if !strings.Contains(out, "THINKING") {
		t.Errorf("expected THINKING in status display, got %q", out)
	}
}

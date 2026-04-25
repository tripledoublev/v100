package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
)

func TestComputePaneLayoutAppliesWidthMinimums(t *testing.T) {
	layout := computePaneLayout(90, 24, 66, 50)
	if layout.leftWidth < 38 {
		t.Fatalf("left width = %d, want >= 38", layout.leftWidth)
	}
	if layout.rightWidth < 24 {
		t.Fatalf("right width = %d, want >= 24", layout.rightWidth)
	}
	if layout.transcriptWidth != layout.leftWidth-4 {
		t.Fatalf("transcript width = %d, want %d", layout.transcriptWidth, layout.leftWidth-4)
	}
}

func TestComputePaneLayoutAppliesHeightMinimums(t *testing.T) {
	layout := computePaneLayout(120, 2, 66, 50)
	if layout.remainingHeight != 4 {
		t.Fatalf("remaining height = %d, want 4", layout.remainingHeight)
	}
	if layout.transcriptHeight < 1 {
		t.Fatalf("transcript height = %d, want >= 1", layout.transcriptHeight)
	}
	if layout.traceViewportHeight < 1 {
		t.Fatalf("trace viewport height = %d, want >= 1", layout.traceViewportHeight)
	}
	if layout.maxStatusContentHeight < 4 {
		t.Fatalf("status content height = %d, want >= 4", layout.maxStatusContentHeight)
	}
}

func TestPaneLayoutWithRightColumnHeightsRespectsMinimumTraceHeight(t *testing.T) {
	base := computePaneLayout(140, 20, 66, 50)
	layout := base.withRightColumnHeights(8, 10)
	if layout.traceContentHeight < 2 {
		t.Fatalf("trace content height = %d, want >= 2", layout.traceContentHeight)
	}
	if layout.traceViewportHeight < 1 {
		t.Fatalf("trace viewport height = %d, want >= 1", layout.traceViewportHeight)
	}
}

func TestPaneLayoutWithRightColumnHeightsFillsRemainingColumnHeight(t *testing.T) {
	base := computePaneLayout(140, 30, 66, 50)
	layout := base.withRightColumnHeights(12, 8)
	traceRendered := layout.traceContentHeight + 2
	if got, want := traceRendered+12+8, layout.remainingHeight; got != want {
		t.Fatalf("right column rendered height = %d, want %d", got, want)
	}
}

func TestComputeHeaderLayoutKeepsHeaderWithinWidth(t *testing.T) {
	header := computeHeaderLayout(72, time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC))
	rendered := renderHeader(72, header)
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > 72 {
			t.Fatalf("header line exceeds width: %q", line)
		}
	}
	if header.clockText != "09:00:00" {
		t.Fatalf("clock text = %q, want 09:00:00", header.clockText)
	}
}

func TestComputeViewLayoutTracksSplitAndSinglePaneModes(t *testing.T) {
	split := computeViewLayout(140, 30, 3, 1, 66, 50, true, time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC))
	if !split.showSplit {
		t.Fatal("expected split layout when trace is enabled")
	}
	if split.panes.leftWidth == 0 || split.panes.rightWidth == 0 {
		t.Fatalf("expected pane widths in split layout, got %+v", split.panes)
	}

	single := computeViewLayout(100, 24, 3, 1, 66, 50, false, time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC))
	if single.showSplit {
		t.Fatal("expected single-pane layout when trace is disabled")
	}
	if single.inputWidth != 98 {
		t.Fatalf("input width = %d, want 98", single.inputWidth)
	}
}

func TestViewSnapshotsForCoreScreenSizes(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want string
		contains []string
	}{
		{
			name: "narrow",
			w:    92,
			h:    26,
			want: `v100  Tab:focus  Ctrl+PgUp/PgDn:half  Ctrl+M:inspect  Ctrl+D:detail  Ctrl+A:copy …  <clock>
╭─────────────────────────────────────────────────────────╮ ╭──────────────────────────────╮
│transcript line one                                      │ │trace                         │
│transcript line two                                      │ │trace line one                │
│                                                         │ ╰──────────────────────────────╯
│                                                         │ ╭──────────────────────────────╮
│                                                         │ │visual inspector              │
│                                                         │ │path: …ai/v100/internal/ui    │
│                                                         │ │STEPS [█████··············]   │
│                                                         │ │TOKEN [██·················]   │
│                                                         │ │REAS. [███████············]   │
│                                                         │ │COST  [···················]   │
│                                                         │ │velocity: hot  model:4/30s    │
│                                                         │ │tools:7/30s  compress:1/30s   │
│                                                         │ │health: compression-pressure  │
│                                                         │ │token:15%  io:42%             │
│                                                         │ │state: thinking  idle:<dur>      │
│                                                         │ │last step: 2s  tools:2        │
│                                                         │ │HEARTBEAT: [──···Λ····──]     │
│                                                         │ ╰──────────────────────────────╯
│                                                         │ ╭──────────────────────────────╮
│                                                         │ │status                        │
│                                                         │ │Snapshot coverage for transc  │
╰─────────────────────────────────────────────────────────╯ │ript, trace, inspector, and   │
                                                            │status panes.                 │
                                                            │THINKING                      │
                                                            │Testing representative TUI s  │
                                                            ╰──────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────╮
│> ask v100 to inspect, patch, or debug...                                                 │
╰──────────────────────────────────────────────────────────────────────────────────────────╯`,
			contains: []string{
				"│visual inspector              │",
				"│path: …ai/v100/internal/ui    │",
				"│status                        │",
				"│Testing representative TUI s  │",
			},
		},
		{
			name: "standard",
			w:    120,
			h:    30,
			want: `v100  Tab:focus  Shift+Tab:back  Ctrl+PgUp/PgDn:half  Ctrl+T:trace  Ctrl+S:status  Ctrl+M:inspector  Ctrl+D:d…  <clock>
╭───────────────────────────────────────────────────────────────────────────╮ ╭────────────────────────────────────────╮
│transcript line one                                                        │ │trace                                   │
│transcript line two                                                        │ │trace line one                          │
│                                                                           │ ╰────────────────────────────────────────╯
│                                                                           │ ╭────────────────────────────────────────╮
│                                                                           │ │visual inspector                        │
│                                                                           │ │path: …me/v/main/ai/v100/internal/ui    │
│                                                                           │ │STEPS [████████·····················]   │
│                                                                           │ │TOKEN [████·························]   │
│                                                                           │ │REAS. [████████████·················]   │
│                                                                           │ │COST  [·····························]   │
│                                                                           │ │velocity: hot  model:4/30s  tools:7/30s │
│                                                                           │ │compress:1/30s                          │
│                                                                           │ │health: compression-pressure  token:15% │
│                                                                           │ │io:42%                                  │
│                                                                           │ │state: thinking  idle:<dur>                │
│                                                                           │ │last step: 2s  tools:2                  │
│                                                                           │ │HEARTBEAT: [──···Λ····──]               │
│                                                                           │ ╰────────────────────────────────────────╯
│                                                                           │ ╭────────────────────────────────────────╮
│                                                                           │ │status                                  │
│                                                                           │ │Snapshot coverage for transcript, trac  │
│                                                                           │ │e, inspector, and status panes.         │
│                                                                           │ │THINKING                                │
│                                                                           │ │Testing representative TUI sizes.       │
╰───────────────────────────────────────────────────────────────────────────╯ │device: charging 84%                    │
                                                                              │                                        │
                                                                              │sub-agents: active=0 done=0 failed=0    │
                                                                              ╰────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│> ask v100 to inspect, patch, or debug...                                                                             │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯`,
		},
		{
			name: "wide",
			w:    160,
			h:    36,
			want: `v100  Tab:focus  Shift+Tab:back  Ctrl+PgUp/PgDn:half  Shift+Arrows:resize  Ctrl+T:trace  Ctrl+S:status  Ctrl+M:inspector  Ctrl+D:detail  Ctrl+A:copy …  <clock>
╭──────────────────────────────────────────────────────────────────────────────────────────────────────╮ ╭─────────────────────────────────────────────────────╮
│transcript line one                                                                                   │ │trace                                                │
│transcript line two                                                                                   │ │trace line one                                       │
│                                                                                                      │ │trace line two                                       │
│                                                                                                      │ │                                                     │
│                                                                                                      │ ╰─────────────────────────────────────────────────────╯
│                                                                                                      │ ╭─────────────────────────────────────────────────────╮
│                                                                                                      │ │visual inspector                                     │
│                                                                                                      │ │path: /home/v/main/ai/v100/internal/ui               │
│                                                                                                      │ │STEPS [████████████······························]   │
│                                                                                                      │ │TOKEN [██████····································]   │
│                                                                                                      │ │REAS. [█████████████████·························]   │
│                                                                                                      │ │COST  [··········································]   │
│                                                                                                      │ │velocity: hot  model:4/30s  tools:7/30s              │
│                                                                                                      │ │compress:1/30s                                       │
│                                                                                                      │ │health: compression-pressure  token:15%  io:42%      │
│                                                                                                      │ │state: thinking  idle:<dur>                             │
│                                                                                                      │ │last step: 2s  tools:2                               │
│                                                                                                      │ │HEARTBEAT: [──···Λ····──]                            │
│                                                                                                      │ ╰─────────────────────────────────────────────────────╯
│                                                                                                      │ ╭─────────────────────────────────────────────────────╮
│                                                                                                      │ │status                                               │
│                                                                                                      │ │Snapshot coverage for transcript, trace, inspector,  │
│                                                                                                      │ │and status panes.                                    │
│                                                                                                      │ │THINKING                                             │
│                                                                                                      │ │Testing representative TUI sizes.                    │
│                                                                                                      │ │device: charging 84%                                 │
│                                                                                                      │ │                                                     │
│                                                                                                      │ │sub-agents: active=0 done=0 failed=0                 │
│                                                                                                      │ │last: none                                           │
│                                                                                                      │ │                                                     │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────╯ ╰─────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│> ask v100 to inspect, patch, or debug...                                                                                                                     │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := snapshotFixtureModel(tc.w, tc.h)
			got := normalizeViewSnapshot(m.View())
			if len(tc.contains) > 0 {
				for _, fragment := range tc.contains {
					if !strings.Contains(got, fragment) {
						t.Fatalf("snapshot missing fragment for %s\nfragment: %q\n--- got ---\n%s", tc.name, fragment, got)
					}
				}
				return
			}
			if got != tc.want {
				t.Fatalf("snapshot mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, tc.want)
			}
		})
	}
}

func snapshotFixtureModel(width, height int) *TUIModel {
	m := NewTUIModel(false, false)
	m.width = width
	m.height = height
	m.showTrace = true
	m.showStatus = true
	m.showMetrics = true
	m.statusMode = "thinking"
	m.lastEventAt = time.Now().Add(-3 * time.Second)
	m.currentStep = 3
	m.maxSteps = 10
	m.usedTokens = 1200
	m.maxTokens = 8000
	m.inputTokens = 700
	m.outputTokens = 500
	m.usedCost = 0.01
	m.maxCost = 1.0
	m.lastStepMS = 2400
	m.lastStepTools = 2
	m.modelEvents = make([]time.Time, 4)
	m.toolEvents = make([]time.Time, 7)
	m.compressEvents = make([]time.Time, 1)
	m.runSummary = "Snapshot coverage for transcript, trace, inspector, and status panes."
	m.statusLine = "Testing representative TUI sizes."
	m.WorkspacePath = "/home/v/main/ai/v100/internal/ui"
	m.device = deviceStatus{BatteryPresent: true, Percent: 84, State: "charging"}
	m.radioURL = availableStations[0].URL
	m.radioVolume = 60
	m.radioPlaying = false
	m.transcript.SetContent("transcript line one\ntranscript line two")
	m.traceView.SetContent("trace line one\ntrace line two")
	return m
}

func normalizeViewSnapshot(view string) string {
	view = stripANSI(view)
	view = regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`).ReplaceAllString(view, "<clock>")
	view = regexp.MustCompile(`idle:\S+`).ReplaceAllString(view, "idle:<dur>")
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

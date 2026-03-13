package ui

import (
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
	layout := base.withRightColumnHeights(8, 10, 50)
	if layout.traceContentHeight < 2 {
		t.Fatalf("trace content height = %d, want >= 2", layout.traceContentHeight)
	}
	if layout.traceViewportHeight < 1 {
		t.Fatalf("trace viewport height = %d, want >= 1", layout.traceViewportHeight)
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

package ui

import "testing"

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

package ui

type paneLayout struct {
	remainingHeight        int
	leftWidth              int
	rightWidth             int
	transcriptWidth        int
	transcriptHeight       int
	traceContentHeight     int
	traceViewportHeight    int
	maxStatusContentHeight int
}

func computePaneLayout(totalWidth, totalHeight, leftPanePct, tracePanePct int) paneLayout {
	layout := paneLayout{
		remainingHeight: totalHeight,
	}

	if layout.remainingHeight < 4 {
		layout.remainingHeight = 4
	}

	if totalWidth <= 0 {
		return layout
	}

	// Each pane has 2 border columns and the split adds a 1-column gap.
	availableWidth := totalWidth - 5
	leftWidth := availableWidth * leftPanePct / 100
	if leftWidth < 38 {
		leftWidth = 38
	}
	rightWidth := availableWidth - leftWidth
	if rightWidth < 24 {
		rightWidth = 24
		leftWidth = availableWidth - rightWidth
	}
	if leftWidth < 24 {
		leftWidth = 24
	}
	if rightWidth < 24 {
		rightWidth = 24
	}

	layout.leftWidth = leftWidth
	layout.rightWidth = rightWidth
	layout.transcriptWidth = max(1, leftWidth-4)
	layout.transcriptHeight = max(1, layout.remainingHeight-2)

	traceRendered := layout.remainingHeight * tracePanePct / 100
	if traceRendered < 4 {
		traceRendered = 4
	}
	traceContentHeight := max(1, traceRendered-2)
	layout.traceContentHeight = traceContentHeight
	layout.traceViewportHeight = max(1, traceContentHeight-1)

	maxStatusContentHeight := (layout.remainingHeight * 2 / 5) - 2
	if maxStatusContentHeight < 4 {
		maxStatusContentHeight = 4
	}
	layout.maxStatusContentHeight = maxStatusContentHeight

	return layout
}

func (p paneLayout) withRightColumnHeights(metricsRendered, statusRendered, tracePanePct int) paneLayout {
	traceRemaining := p.remainingHeight - metricsRendered - statusRendered
	traceRendered := traceRemaining * tracePanePct / 100
	if traceRendered < 4 {
		traceRendered = 4
	}
	p.traceContentHeight = max(1, traceRendered-2)
	p.traceViewportHeight = max(1, p.traceContentHeight-1)
	return p
}

package ui

import (
	"strings"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/i18n"
)

// Border overhead constants: how many columns/rows are consumed by lipgloss borders and padding.
const (
	splitBorderCols  = 5 // Each pane has 2 border cols, and split adds a 1-col gap: 2 + 2 + 1
	singleBorderSize = 2 // Single pane borders consume 2 rows/cols
)

type headerLayout struct {
	leftText  string
	clockText string
	gapWidth  int
}

type viewLayoutPlan struct {
	header           headerLayout
	inputWidth       int
	leftPaneHeight   int
	singlePaneHeight int
	remainingHeight  int
	showSplit        bool
	panes            paneLayout
}

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
	availableWidth := totalWidth - splitBorderCols
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

func (p paneLayout) withRightColumnHeights(metricsRendered, statusRendered int) paneLayout {
	traceRendered := p.remainingHeight - metricsRendered - statusRendered
	if traceRendered < 4 {
		traceRendered = 4
	}
	p.traceContentHeight = max(1, traceRendered-2)
	p.traceViewportHeight = max(1, p.traceContentHeight-1)
	return p
}

func computeHeaderLayout(totalWidth int, now time.Time) headerLayout {
	// Wide hint with all controls
	headerHint := i18n.T("ui_header_hint_wide")
	// Medium hint - drop resize hint
	if totalWidth < 140 {
		headerHint = i18n.T("ui_header_hint_medium")
	}
	// Narrow hint - drop status and trace toggles
	if totalWidth < 110 {
		headerHint = i18n.T("ui_header_hint_narrow")
	}
	if totalWidth < 95 {
		headerHint = i18n.T("ui_header_hint_tiny")
	}

	leftText := "v100" + headerHint
	clockText := now.Format("15:04:05")
	minGap := 2
	if totalWidth > len(clockText)+minGap {
		maxLeft := totalWidth - len(clockText) - minGap
		if maxLeft < 4 {
			maxLeft = 4
		}
		if lipgloss.Width(leftText) > maxLeft {
			leftText = truncateHeaderText(leftText, maxLeft)
		}
	}

	return headerLayout{
		leftText:  leftText,
		clockText: clockText,
		gapWidth:  max(0, totalWidth-lipgloss.Width(leftText)-len(clockText)),
	}
}

func computeViewLayout(totalWidth, totalHeight, inputHeight, headerHeight, leftPanePct, tracePanePct int, showTrace bool, now time.Time) viewLayoutPlan {
	remainingHeight := totalHeight - headerHeight - inputHeight
	plan := viewLayoutPlan{
		header:           computeHeaderLayout(totalWidth, now),
		inputWidth:       max(1, totalWidth-singleBorderSize),
		leftPaneHeight:   max(1, remainingHeight-singleBorderSize),
		singlePaneHeight: max(1, remainingHeight-singleBorderSize),
		remainingHeight:  remainingHeight,
		showSplit:        showTrace,
	}
	plan.panes = computePaneLayout(totalWidth, remainingHeight, leftPanePct, tracePanePct)
	plan.remainingHeight = plan.panes.remainingHeight
	return plan
}

func renderHeader(totalWidth int, header headerLayout) string {
	return lipgloss.NewStyle().Width(totalWidth).MaxWidth(totalWidth).Render(
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			tuiHeaderStyle.Render(header.leftText),
			tuiHeaderDimStyle.Render(strings.Repeat(" ", header.gapWidth)),
			tuiHeaderDimStyle.Render(header.clockText),
		),
	)
}

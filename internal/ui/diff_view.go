package ui

import (
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
)

const diffPrefixWidth = 4

func diffSegmentStyle(seg eval.SyncSegment, side string) lipgloss.Style {
	switch seg.Status {
	case eval.SegmentMatch:
		return styleMuted
	case eval.SegmentDiverge:
		return styleFail
	case eval.SegmentTailA:
		if side == "A" {
			return styleInfo
		}
		return styleMuted
	case eval.SegmentTailB:
		if side == "B" {
			return styleWarn
		}
		return styleMuted
	default:
		return lipgloss.NewStyle()
	}
}

func diffSegmentPrefix(sd eval.SyncDiff, idx int) string {
	if sd.DivergeIndex >= 0 && idx == sd.DivergeIndex {
		return ">>> "
	}
	return fmt.Sprintf("%3d ", idx)
}

func buildDiffPaneContents(sd eval.SyncDiff, leftWidth, rightWidth int) (string, string, int) {
	if len(sd.Segments) == 0 {
		empty := styleMuted.Render("no events")
		return empty, empty, -1
	}

	leftContentWidth := max(1, leftWidth-diffPrefixWidth)
	rightContentWidth := max(1, rightWidth-diffPrefixWidth)
	blankPrefix := strings.Repeat(" ", diffPrefixWidth)
	var leftLines []string
	var rightLines []string
	divergeLine := -1

	for idx, seg := range sd.Segments {
		leftBlock := renderTraceEventBlock(seg.EventA, leftContentWidth)
		rightBlock := renderTraceEventBlock(seg.EventB, rightContentWidth)
		rows := max(len(leftBlock), len(rightBlock))
		if rows == 0 {
			rows = 1
		}
		if sd.DivergeIndex >= 0 && idx == sd.DivergeIndex && divergeLine < 0 {
			divergeLine = len(leftLines)
		}

		for row := 0; row < rows; row++ {
			prefix := blankPrefix
			if row == 0 {
				prefix = diffSegmentPrefix(sd, idx)
			}
			leftText := ""
			rightText := ""
			if row < len(leftBlock) {
				leftText = leftBlock[row]
			}
			if row < len(rightBlock) {
				rightText = rightBlock[row]
			}
			leftLines = append(leftLines, diffSegmentStyle(seg, "A").Render(prefix+leftText))
			rightLines = append(rightLines, diffSegmentStyle(seg, "B").Render(prefix+rightText))
		}
	}

	return strings.Join(leftLines, "\n"), strings.Join(rightLines, "\n"), divergeLine
}

func renderDiffEventBlock(ev *core.Event, width int) []string {
	return renderTraceEventBlock(ev, width)
}

// renderDiffHeader renders the top header for the diff TUI.
func renderDiffHeader(sd eval.SyncDiff, width int) string {
	title := fmt.Sprintf("v100 diff  %s vs %s", sd.RunA, sd.RunB)
	info := fmt.Sprintf("segments: %d  common: %d  diverge: %s",
		len(sd.Segments), len(sd.CommonPrefix()), sd.DivergeType)
	if sd.DiffEvidence != "" {
		info += "  " + sd.DiffEvidence
	}

	header := tuiHeaderStyle.Render(title)
	if lipgloss.Width(header) < width {
		gap := width - lipgloss.Width(header) - lipgloss.Width(info)
		if gap > 2 {
			header += strings.Repeat(" ", gap) + tuiHeaderDimStyle.Render(info)
		}
	}

	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(header)
}

// renderDiffFooter renders navigation hints at the bottom.
func renderDiffFooter(width int) string {
	hints := "↑/↓/j/k scroll  pgup/pgdn/home/end jump  d divergence  q quit"
	return tuiHeaderDimStyle.Width(width).Render(hints)
}

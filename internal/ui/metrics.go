package ui

import (
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
)

// MetricBarConfig holds configuration for ASCII metric bars.
type MetricBarConfig struct {
	Width          int  // total width of the bar in characters
	ShowPercentage bool // show percentage label
	ShowValues     bool // show numeric values
	EmptyChar      rune // character for empty portion
	FullChar       rune // character for filled portion
	TruncatedChar  rune // character for truncated portion (when value exceeds max)
}

// DefaultMetricBarConfig returns a sensible default configuration.
func DefaultMetricBarConfig() MetricBarConfig {
	return MetricBarConfig{
		Width:          20,
		ShowPercentage: true,
		ShowValues:     true,
		EmptyChar:      '░',
		FullChar:       '▓',
		TruncatedChar:  '█',
	}
}

// TokenBudgetBar renders an ASCII bar showing token budget consumption.
// Shows input/output split when both are provided, otherwise shows total usage.
func TokenBudgetBar(used, max, inputUsed, outputUsed int, cfg MetricBarConfig) string {
	if max <= 0 {
		return styleMuted.Render("tokens: unlimited")
	}

	cfg.Width = constrainWidth(cfg.Width, 10, 40)
	used = min(used, max)
	percent := float64(used) / float64(max)

	bar := renderBar(percent, cfg)

	var line string
	if inputUsed > 0 || outputUsed > 0 {
		// Show split: input | output
		inputPercent := 0.0
		if used > 0 {
			inputPercent = float64(inputUsed) / float64(used)
		}
		inputWidth := int(float64(cfg.Width) * inputPercent)
		outputWidth := cfg.Width - inputWidth

		inBar := renderBarSegment(inputWidth, cfg, true)
		outBar := renderBarSegment(outputWidth, cfg, false)

		line = fmt.Sprintf("tokens [%s│%s] %d/%d",
			inBar, outBar, used, max)
	} else {
		line = fmt.Sprintf("tokens [%s] %d/%d", bar, used, max)
	}

	if cfg.ShowPercentage {
		pctLabel := fmt.Sprintf("%.0f%%", percent*100)
		line = fmt.Sprintf("%s %s", line, styleMuted.Render(pctLabel))
	}

	return line
}

// StepProgressBar renders an ASCII bar showing step progress.
func StepProgressBar(current, max int, cfg MetricBarConfig) string {
	if max <= 0 {
		return stylePrimary.Render("steps: running...")
	}

	cfg.Width = constrainWidth(cfg.Width, 10, 40)
	current = min(current, max)
	percent := float64(current) / float64(max)

	bar := renderBar(percent, cfg)

	line := fmt.Sprintf("steps [%s] %d/%d", bar, current, max)

	if cfg.ShowPercentage {
		pctLabel := fmt.Sprintf("%.0f%%", percent*100)
		line = fmt.Sprintf("%s %s", line, styleMuted.Render(pctLabel))
	}

	// Add indicator if at or near limit
	if current >= max {
		line = line + " " + styleWarn.Render("⚠")
	} else if percent >= 0.8 {
		line = line + " " + styleInfo.Render("●")
	}

	return line
}

// CostBar renders an ASCII bar showing cost budget consumption.
func CostBar(used, max float64, cfg MetricBarConfig) string {
	if max <= 0 {
		return styleMuted.Render("cost: unlimited")
	}

	cfg.Width = constrainWidth(cfg.Width, 10, 40)
	used = min(used, max)
	percent := used / max

	bar := renderBar(percent, cfg)

	line := fmt.Sprintf("cost  [%s] $%.4f/$%.4f", bar, used, max)

	if cfg.ShowPercentage {
		pctLabel := fmt.Sprintf("%.0f%%", percent*100)
		line = fmt.Sprintf("%s %s", line, styleMuted.Render(pctLabel))
	}

	return line
}

// MultiMetricBar renders multiple metrics in a compact stacked layout.
// Useful for displaying a summary dashboard.
type MetricItem struct {
	Label string
	Used  int
	Max   int
	Color func(string) string // optional color function
}

func MultiMetricBar(items []MetricItem, width int) string {
	width = constrainWidth(width, 20, 50)

	var lines []string
	for _, item := range items {
		if item.Max <= 0 {
			lines = append(lines, styleMuted.Render(fmt.Sprintf("%s: unlimited", item.Label)))
			continue
		}

		used := min(item.Used, item.Max)
		percent := float64(used) / float64(item.Max)
		bar := renderBar(percent, MetricBarConfig{
			Width:          width,
			ShowPercentage: false,
			ShowValues:     false,
			EmptyChar:      '░',
			FullChar:       '▓',
		})

		content := fmt.Sprintf("%s [%s] %d/%d", item.Label, bar, used, item.Max)
		if item.Color != nil {
			lines = append(lines, item.Color(content))
		} else {
			lines = append(lines, content)
		}
	}

	return strings.Join(lines, "\n")
}

// renderBar renders a single ASCII progress bar.
func renderBar(percent float64, cfg MetricBarConfig) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}

	filled := int(float64(cfg.Width) * percent)
	empty := cfg.Width - filled

	var b strings.Builder
	b.Grow(cfg.Width)

	// Filled portion
	for i := 0; i < filled; i++ {
		b.WriteRune(cfg.FullChar)
	}

	// Empty portion
	for i := 0; i < empty; i++ {
		b.WriteRune(cfg.EmptyChar)
	}

	return b.String()
}

// renderBarSegment renders a segment of a bar (for split views like input/output).
func renderBarSegment(width int, cfg MetricBarConfig, isInput bool) string {
	if width <= 0 {
		return strings.Repeat(string(cfg.EmptyChar), cfg.Width)
	}
	if width > cfg.Width {
		width = cfg.Width
	}

	var b strings.Builder
	b.Grow(cfg.Width)

	char := cfg.FullChar
	if !isInput {
		// Use a different character for output to visually distinguish
		char = '▒'
	}

	for i := 0; i < width; i++ {
		b.WriteRune(char)
	}

	for i := width; i < cfg.Width; i++ {
		b.WriteRune(cfg.EmptyChar)
	}

	return b.String()
}

// constrainWidth ensures width is within bounds.
func constrainWidth(width, min, max int) int {
	if width < min {
		return min
	}
	if width > max {
		return max
	}
	return width
}

// LiveMetricDashboard renders a complete live metrics dashboard with
// token budget burn-down and step progress visualization.
func LiveMetricDashboard(currentStep, maxSteps, usedTokens, maxTokens, inputTokens, outputTokens int, usedCost, maxCost float64, width int) string {
	width = constrainWidth(width, 30, 60)

	cfg := DefaultMetricBarConfig()
	cfg.Width = (width - 20) / 2 // Leave room for labels
	if cfg.Width < 10 {
		cfg.Width = 10
	}

	var lines []string

	// Header
	lines = append(lines, styleBold.Render("┌─ live metrics ────────────────────────────────┐"))

	// Step progress
	stepLine := StepProgressBar(currentStep, maxSteps, cfg)
	lines = append(lines, stylePrimary.Render("│ ")+stepLine+strings.Repeat(" ", max(0, width-lipgloss.Width(stepLine))))

	// Token budget (split view if we have input/output)
	tokenLine := TokenBudgetBar(usedTokens, maxTokens, inputTokens, outputTokens, cfg)
	lines = append(lines, stylePrimary.Render("│ ")+tokenLine+strings.Repeat(" ", max(0, width-lipgloss.Width(tokenLine))))

	// Cost
	costLine := CostBar(usedCost, maxCost, cfg)
	lines = append(lines, stylePrimary.Render("│ ")+costLine+strings.Repeat(" ", max(0, width-lipgloss.Width(costLine))))

	// Footer
	lines = append(lines, styleBold.Render("└─────────────────────────────────────────────────┘"))

	return strings.Join(lines, "\n")
}

// stripStyles is no longer needed since we use lipgloss.Width.

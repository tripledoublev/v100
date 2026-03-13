package ui

import (
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
)

// LiveMetricDashboard renders the "Gaming Minimap" style visual inspector.
func LiveMetricDashboard(currentStep, maxSteps, usedTokens, maxTokens, inTokens, outTokens int, usedCost, maxCost float64, lastStepMS int64, lastStepTools, recentModelCalls, recentToolCalls, recentCompresses, width int) string {
	if width < 20 {
		return ""
	}

	// Define colors locally to avoid undefined global errors
	colorPrimary := lipgloss.Color("#3B82F6")
	colorInfo := lipgloss.Color("#06B6D4")
	colorWarn := lipgloss.Color("#F59E0B")
	colorDanger := lipgloss.Color("#EF4444")
	colorCognition := lipgloss.Color("#A78BFA")
	colorMoney := lipgloss.Color("#10B981")

	w := width - 4
	
	// 1. Step Progress (The "Shield/Fuel" Meter)
	stepPct := 0.0
	if maxSteps > 0 {
		stepPct = float64(currentStep) / float64(maxSteps)
	}
	stepBar := renderMiniBar("STEPS", stepPct, w, colorPrimary)

	// 2. Token Entropy (The "System Pressure" Meter)
	tokenPct := 0.0
	if maxTokens > 0 {
		tokenPct = float64(usedTokens) / float64(maxTokens)
	}
	tokenColor := colorInfo
	if tokenPct > 0.7 {
		tokenColor = colorWarn
	}
	if tokenPct > 0.9 {
		tokenColor = colorDanger
	}
	tokenBar := renderMiniBar("TOKEN", tokenPct, w, tokenColor)

	// 3. I/O Ratio (The "Reasoning Balance" Indicator)
	ioTotal := inTokens + outTokens
	ioRatio := 0.5
	if ioTotal > 0 {
		ioRatio = float64(outTokens) / float64(ioTotal)
	}
	ioBar := renderMiniBar("REAS.", ioRatio, w, colorCognition)

	// 4. Cost Vector
	costPct := 0.0
	if maxCost > 0.0001 { // avoid div by zero
		costPct = usedCost / maxCost
	}
	costBar := renderMiniBar("COST ", costPct, w, colorMoney)

	// 5. Radar/Heartbeat (Visual Flair)
	pulse := currentStep % 8
	heartbeat := "──" + strings.Repeat("·", pulse) + "Λ" + strings.Repeat("·", (7-pulse)) + "──"
	heartbeat = styleMuted.Render("[") + styleInfo.Render(heartbeat) + styleMuted.Render("]")

	tempo := "cool"
	if recentToolCalls >= 6 || recentModelCalls >= 4 {
		tempo = "hot"
	} else if recentToolCalls >= 3 || recentModelCalls >= 2 {
		tempo = "warm"
	}
	velocityLine := fmt.Sprintf("velocity: %s  model:%d/30s  tools:%d/30s  compress:%d/30s",
		tempo, recentModelCalls, recentToolCalls, recentCompresses)
	lastStepLine := fmt.Sprintf("last step: %s  tools:%d", FormatDuration(lastStepMS), lastStepTools)

	dashboard := lipgloss.JoinVertical(lipgloss.Left,
		tuiStatusLabelStyle.Render("visual inspector"),
		stepBar,
		tokenBar,
		ioBar,
		costBar,
		styleMuted.Render(velocityLine),
		styleMuted.Render(lastStepLine),
		fmt.Sprintf("%s %s", styleMuted.Render("HEARTBEAT:"), heartbeat),
	)

	return dashboard
}

func renderMiniBar(label string, pct float64, width int, color lipgloss.Color) string {
	// Account for label, space, and trailing bracket
	barWidth := width - lipgloss.Width(label) - 2
	if barWidth < 5 {
		return label
	}

	filled := int(float64(barWidth) * pct)
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	empty := barWidth - filled
	
	barStyle := lipgloss.NewStyle().Foreground(color)
	
	return fmt.Sprintf("%s %s%s%s%s",
		styleMuted.Render(label),
		styleMuted.Render("["),
		barStyle.Render(strings.Repeat("█", filled)),
		styleMuted.Render(strings.Repeat("░", empty)),
		styleMuted.Render("]"),
	)
}

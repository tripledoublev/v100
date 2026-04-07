package ui

import (
	"fmt"
	"strings"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
)

// LiveMetricDashboard renders the "Gaming Minimap" style visual inspector.
func LiveMetricDashboard(currentStep, maxSteps, usedTokens, maxTokens, inTokens, outTokens int, usedCost, maxCost float64, lastStepMS int64, lastStepTools, recentModelCalls, recentToolCalls, recentCompresses int, statusMode string, idleFor time.Duration, width int) string {
	if width < 20 {
		return ""
	}

	// Define colors locally for the dashboard
	colorPrimary := lipgloss.Color("#3B82F6")
	colorInfo := lipgloss.Color("#06B6D4")
	colorWarn := lipgloss.Color("#F59E0B")
	colorDanger := lipgloss.Color("#EF4444")
	colorCognition := lipgloss.Color("#A78BFA")
	colorMoney := lipgloss.Color("#10B981")

	// Create local styles for consistent rendering
	styleBlue := lipgloss.NewStyle().Foreground(colorPrimary)
	styleCyan := lipgloss.NewStyle().Foreground(colorInfo)
	styleAmber := lipgloss.NewStyle().Foreground(colorWarn)
	styleRed := lipgloss.NewStyle().Foreground(colorDanger)
	styleViolet := lipgloss.NewStyle().Foreground(colorCognition)
	styleGreen := lipgloss.NewStyle().Foreground(colorMoney)

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
	tokenStyle := styleCyan
	if tokenPct > 0.7 {
		tokenColor = colorWarn
		tokenStyle = styleAmber
	}
	if tokenPct > 0.9 {
		tokenColor = colorDanger
		tokenStyle = styleRed
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
	heartbeat = styleMuted.Render("[") + styleCyan.Bold(true).Render(heartbeat) + styleMuted.Render("]")

	tempo := "cool"
	if recentToolCalls >= 6 || recentModelCalls >= 4 {
		tempo = "hot"
	} else if recentToolCalls >= 3 || recentModelCalls >= 2 {
		tempo = "warm"
	}
	health := "stable"
	switch {
	case tokenPct >= 0.9:
		health = "critical"
	case recentCompresses > 0 || tokenPct >= 0.75:
		health = "compression-pressure"
	case tokenPct >= 0.5 || recentToolCalls >= 4:
		health = "warming"
	}
	velocityColor := styleMuted
	switch tempo {
	case "hot":
		velocityColor = styleRed
	case "warm":
		velocityColor = styleAmber
	case "cool":
		velocityColor = styleCyan
	}

	healthColor := styleMuted
	switch health {
	case "critical":
		healthColor = styleRed
	case "compression-pressure":
		healthColor = styleAmber
	case "warming":
		healthColor = styleCyan
	case "stable":
		healthColor = styleGreen
	}

	stateColor := styleMuted
	switch inspectorState(statusMode, idleFor) {
	case "thinking":
		stateColor = styleViolet
	case "tooling":
		stateColor = styleAmber
	case "error":
		stateColor = styleRed
	case "ready":
		stateColor = styleGreen
	}

	velocityLine := fmt.Sprintf("%s %s  %s%s/30s  %s%s/30s  %s%s/30s",
		styleMuted.Render("velocity:"), velocityColor.Bold(true).Render(tempo),
		styleMuted.Render("model:"), styleBlue.Bold(true).Render(fmt.Sprintf("%d", recentModelCalls)),
		styleMuted.Render("tools:"), styleAmber.Bold(true).Render(fmt.Sprintf("%d", recentToolCalls)),
		styleMuted.Render("compress:"), styleRed.Bold(true).Render(fmt.Sprintf("%d", recentCompresses)))

	pressureLine := fmt.Sprintf("%s %s  %s%s  %s%s",
		styleMuted.Render("health:"), healthColor.Bold(true).Render(health),
		styleMuted.Render("token:"), tokenStyle.Bold(true).Render(percentLabel(tokenPct)),
		styleMuted.Render("io:"), styleViolet.Bold(true).Render(percentLabel(ioRatio)))

	stateLine := fmt.Sprintf("%s %s  %s%s",
		styleMuted.Render("state:"), stateColor.Bold(true).Render(inspectorState(statusMode, idleFor)),
		styleMuted.Render("idle:"), styleCyan.Bold(true).Render(FormatDuration(idleFor.Milliseconds())))

	lastStepLine := fmt.Sprintf("%s %s  %s%s",
		styleMuted.Render("last step:"), styleCyan.Bold(true).Render(FormatDuration(lastStepMS)),
		styleMuted.Render("tools:"), styleAmber.Bold(true).Render(fmt.Sprintf("%d", lastStepTools)))

	inspectorTitleStyle := lipgloss.NewStyle().
		Foreground(colorCognition).
		Italic(true).
		Bold(true)

	heartbeatLabelStyle := styleMuted
	switch tempo {
	case "hot":
		heartbeatLabelStyle = styleRed
	case "warm":
		heartbeatLabelStyle = styleAmber
	}

	dashboard := lipgloss.JoinVertical(lipgloss.Left,
		inspectorTitleStyle.Render("visual inspector"),
		stepBar,
		tokenBar,
		ioBar,
		costBar,
		velocityLine,
		pressureLine,
		stateLine,
		lastStepLine,
		fmt.Sprintf("%s %s", heartbeatLabelStyle.Render("HEARTBEAT:"), heartbeat),
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
	labelStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#374151")) // very dark gray

	return fmt.Sprintf("%s %s%s%s%s",
		labelStyle.Render(label),
		styleMuted.Render("["),
		barStyle.Render(strings.Repeat("█", filled)),
		emptyStyle.Render(strings.Repeat("·", empty)),
		styleMuted.Render("]"),
	)
	}

func percentLabel(v float64) string {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return fmt.Sprintf("%d%%", int(v*100+0.5))
}

func inspectorState(statusMode string, idleFor time.Duration) string {
	switch statusMode {
	case "tooling":
		return "tooling"
	case "thinking":
		if idleFor > 10*time.Second {
			return "stalled"
		}
		return "thinking"
	case "error":
		return "error"
	}
	if idleFor > 15*time.Second {
		return "idle"
	}
	return "ready"
}

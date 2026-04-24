package ui

import (
	"fmt"
	"strings"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/i18n"
)

// LiveMetricDashboard renders the "Gaming Minimap" style visual inspector.
func LiveMetricDashboard(currentStep, maxSteps, usedTokens, maxTokens, inTokens, outTokens int, usedCost, maxCost float64, lastStepMS int64, lastStepTools, recentModelCalls, recentToolCalls, recentCompresses int, statusMode string, idleFor time.Duration, width int, workspacePath string) string {
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

	// Workspace path line — truncate from the left if too wide
	pathDisplay := ""
	if workspacePath != "" {
		pathLine := workspacePath
		if maxPathW := w - 6; len(pathLine) > maxPathW {
			pathLine = "…" + pathLine[len(pathLine)-maxPathW+1:]
		}
		pathDisplay = styleMuted.Render(i18n.T("dashboard_path")+": ") + styleCyan.Render(pathLine)
	}

	// 1. Step Progress (The "Shield/Fuel" Meter)
	stepPct := 0.0
	if maxSteps > 0 {
		stepPct = float64(currentStep) / float64(maxSteps)
	}
	stepBar := renderMiniBar(i18n.T("dashboard_steps"), stepPct, w, colorPrimary)

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
	tokenBar := renderMiniBar(i18n.T("dashboard_token"), tokenPct, w, tokenColor)

	// 3. I/O Ratio (The "Reasoning Balance" Indicator)
	ioTotal := inTokens + outTokens
	ioRatio := 0.5
	if ioTotal > 0 {
		ioRatio = float64(outTokens) / float64(ioTotal)
	}
	ioBar := renderMiniBar(i18n.T("dashboard_reasoning"), ioRatio, w, colorCognition)

	// 4. Cost Vector
	costPct := 0.0
	if maxCost > 0.0001 { // avoid div by zero
		costPct = usedCost / maxCost
	}
	costBar := renderMiniBar(i18n.T("dashboard_cost"), costPct, w, colorMoney)

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
		styleMuted.Render(i18n.T("dashboard_velocity")+":"), velocityColor.Bold(true).Render(dashboardLabel(tempo)),
		styleMuted.Render(i18n.T("dashboard_model")+":"), styleBlue.Bold(true).Render(fmt.Sprintf("%d", recentModelCalls)),
		styleMuted.Render(i18n.T("dashboard_tools")+":"), styleAmber.Bold(true).Render(fmt.Sprintf("%d", recentToolCalls)),
		styleMuted.Render(i18n.T("dashboard_compress")+":"), styleRed.Bold(true).Render(fmt.Sprintf("%d", recentCompresses)))

	pressureLine := fmt.Sprintf("%s %s  %s%s  %s%s",
		styleMuted.Render(i18n.T("dashboard_health")+":"), healthColor.Bold(true).Render(dashboardLabel(health)),
		styleMuted.Render(i18n.T("dashboard_token_label")+":"), tokenStyle.Bold(true).Render(percentLabel(tokenPct)),
		styleMuted.Render(i18n.T("dashboard_io")+":"), styleViolet.Bold(true).Render(percentLabel(ioRatio)))

	stateLine := fmt.Sprintf("%s %s  %s%s",
		styleMuted.Render(i18n.T("dashboard_state")+":"), stateColor.Bold(true).Render(dashboardLabel(inspectorState(statusMode, idleFor))),
		styleMuted.Render(i18n.T("dashboard_idle")+":"), styleCyan.Bold(true).Render(FormatDuration(idleFor.Milliseconds())))

	lastStepLine := fmt.Sprintf("%s %s  %s%s",
		styleMuted.Render(i18n.T("dashboard_last_step")+":"), styleCyan.Bold(true).Render(FormatDuration(lastStepMS)),
		styleMuted.Render(i18n.T("dashboard_tools")+":"), styleAmber.Bold(true).Render(fmt.Sprintf("%d", lastStepTools)))

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

	dashboardLines := []string{
		inspectorTitleStyle.Render(i18n.T("dashboard_title")),
	}
	if pathDisplay != "" {
		dashboardLines = append(dashboardLines, pathDisplay)
	}
	dashboardLines = append(dashboardLines,
		stepBar,
		tokenBar,
		ioBar,
		costBar,
		velocityLine,
		pressureLine,
		stateLine,
		lastStepLine,
		fmt.Sprintf("%s %s", heartbeatLabelStyle.Render(i18n.T("dashboard_heartbeat")+":"), heartbeat),
	)

	return lipgloss.JoinVertical(lipgloss.Left, dashboardLines...)
}

func dashboardLabel(key string) string {
	switch key {
	case "cool":
		return i18n.T("dashboard_cool")
	case "warm":
		return i18n.T("dashboard_warm")
	case "hot":
		return i18n.T("dashboard_hot")
	case "stable":
		return i18n.T("dashboard_stable")
	case "critical":
		return i18n.T("dashboard_critical")
	case "compression-pressure":
		return i18n.T("dashboard_pressure")
	case "warming":
		return i18n.T("dashboard_warming")
	case "ready":
		return i18n.T("dashboard_ready")
	case "idle":
		return i18n.T("dashboard_idle")
	case "stalled":
		return i18n.T("dashboard_stalled")
	default:
		return i18n.T("status_" + key)
	}
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

package ui

import (
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

type helpEntry struct {
	Key    string
	Action string
}

func (m *TUIModel) helpView() string {
	boxWidth := m.width - 6
	if boxWidth > 92 {
		boxWidth = 92
	}
	if boxWidth < 34 {
		boxWidth = max(1, m.width-2)
	}
	contentWidth := boxWidth - 6
	if contentWidth < 24 {
		contentWidth = max(1, boxWidth-2)
	}

	contentHeight := m.height - 6
	if contentHeight < 8 {
		contentHeight = max(1, m.height-2)
	}
	content := m.helpContent(contentWidth, contentHeight)
	box := tuiHelpStyle.Width(contentWidth).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m *TUIModel) helpContent(width, maxLines int) string {
	if width < 24 {
		width = 24
	}
	lines := []string{
		stylePrimary.Render("v100 help"),
		styleMuted.Render("Esc, ?, or Ctrl+P closes this view"),
		"",
		styleBold.Render("Actions"),
	}
	for _, entry := range m.helpActions() {
		lines = append(lines, helpRow(entry.Key, entry.Action, width))
	}

	lines = append(lines, "", styleBold.Render("Commands"))
	for _, entry := range m.helpCommands() {
		lines = append(lines, helpRow(entry.Key, entry.Action, width))
	}

	lines = append(lines,
		"",
		styleBold.Render("Current"),
		helpRow("focus", focusName(m.focus), width),
		helpRow("panels", m.helpPanelState(), width),
		helpRow("run", helpValue(m.RunInfo.RunID), width),
		helpRow("model", helpModelValue(m.RunInfo.Provider, m.RunInfo.Model), width),
		helpRow("workspace", helpValue(m.RunInfo.Workspace), width),
		helpRow("trace", helpValue(m.RunInfo.TracePath), width),
		helpRow("confirm", helpValue(m.RunInfo.ConfirmMode), width),
		helpRow("sub-agents", fmt.Sprintf("%d active, %d done, %d failed", len(m.activeAgents), m.agentDoneCount, m.agentFailCount), width),
	)
	if len(m.pastedImages) > 0 {
		lines = append(lines, helpRow("attachments", imageCount(len(m.pastedImages)), width))
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = append(lines[:maxLines-1], styleMuted.Render("..."))
	}
	return strings.Join(lines, "\n")
}

func (m *TUIModel) helpActions() []helpEntry {
	actions := []helpEntry{
		{"Tab / Shift+Tab", "cycle focus"},
		{"Ctrl+PgUp/PgDn", "switch pane half"},
		{"Ctrl+\\", "switch pane half"},
		{"Shift+Arrows", "resize pane"},
		{"Ctrl+T/S/M", "toggle trace/status/inspector"},
	}
	if m.selectedToolExec != nil || m.showDetail {
		actions = append(actions, helpEntry{"Ctrl+D", "toggle tool detail"})
	}
	actions = append(actions,
		helpEntry{"Ctrl+A / Ctrl+V", "copy transcript / attach image"},
		helpEntry{"Alt+R / Ctrl+R", "radio picker / play-stop"},
		helpEntry{"[ / ]", "radio volume"},
	)
	if m.reviewCancel != nil {
		actions = append(actions, helpEntry{"Esc", "cancel active review"})
	} else if m.InterruptFn != nil && (m.statusMode == "thinking" || m.statusMode == "tooling") {
		actions = append(actions, helpEntry{"Esc", "interrupt active step"})
	}
	actions = append(actions, helpEntry{"Ctrl+C", "quit"})
	return actions
}

func (m *TUIModel) helpCommands() []helpEntry {
	commands := []helpEntry{
		{"/model", "switch model or provider"},
		{"/auto", "switch to auto routing mode"},
		{"/local", "switch to local provider"},
		{"/radio", "open radio station picker"},
	}
	if m.radioArtist != "" || m.radioTitle != "" {
		commands = append(commands, helpEntry{"download this song", "save the current radio track"})
	}
	return commands
}

func helpRow(key, value string, width int) string {
	if width < 24 {
		width = 24
	}
	keyWidth := 18
	if width < 52 {
		keyWidth = 12
	}
	valueWidth := width - keyWidth - 2
	if valueWidth < 8 {
		valueWidth = 8
	}
	key = truncateHeaderText(strings.TrimSpace(key), keyWidth)
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unknown"
	}
	wrapped := strings.Split(wrap.String(value, valueWidth), "\n")
	prefix := styleTool.Render(fmt.Sprintf("%-*s", keyWidth, key))
	lines := []string{prefix + "  " + wrapped[0]}
	indent := strings.Repeat(" ", keyWidth+2)
	for _, line := range wrapped[1:] {
		lines = append(lines, indent+line)
	}
	return strings.Join(lines, "\n")
}

func helpValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func helpModelValue(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	switch {
	case provider != "" && model != "":
		return provider + " / " + model
	case provider != "":
		return provider
	case model != "":
		return model
	default:
		return "unknown"
	}
}

func (m *TUIModel) helpPanelState() string {
	detail := "n/a"
	if m.selectedToolExec != nil {
		detail = onOff(m.showDetail)
	}
	return strings.Join([]string{
		"trace:" + onOff(m.showTrace),
		"inspector:" + onOff(m.showMetrics),
		"status:" + onOff(m.showStatus),
		"detail:" + detail,
	}, "  ")
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func focusName(f focus) string {
	switch f {
	case focusTranscript:
		return "transcript"
	case focusInput:
		return "input"
	case focusTrace:
		return "trace"
	case focusStatus:
		return "status"
	case focusDetail:
		return "detail"
	default:
		return "unknown"
	}
}

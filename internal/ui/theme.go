package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme is the semantic color palette used by CLI and TUI renderers.
type Theme struct {
	Name string

	Primary   lipgloss.Color
	Success   lipgloss.Color
	Warning   lipgloss.Color
	Error     lipgloss.Color
	Info      lipgloss.Color
	Muted     lipgloss.Color
	User      lipgloss.Color
	Assistant lipgloss.Color
	Tool      lipgloss.Color
	RunID     lipgloss.Color
	Danger    lipgloss.Color

	PaneBorder       lipgloss.Color
	ActivePaneBorder lipgloss.Color
	CopyIcon         lipgloss.Color

	LatSlow lipgloss.Color
	LatMed  lipgloss.Color
	LatFast lipgloss.Color
}

var builtinThemes = map[string]Theme{
	"v100": {
		Name:             "v100",
		Primary:          lipgloss.Color("#A78BFA"),
		Success:          lipgloss.Color("#34D399"),
		Warning:          lipgloss.Color("#FBBF24"),
		Error:            lipgloss.Color("#F87171"),
		Info:             lipgloss.Color("#60A5FA"),
		Muted:            lipgloss.Color("#6B7280"),
		User:             lipgloss.Color("#C4B5FD"),
		Assistant:        lipgloss.Color("#5EEAD4"),
		Tool:             lipgloss.Color("#FBBF24"),
		RunID:            lipgloss.Color("#818CF8"),
		Danger:           lipgloss.Color("#F87171"),
		PaneBorder:       lipgloss.Color("#374151"),
		ActivePaneBorder: lipgloss.Color("#A78BFA"),
		CopyIcon:         lipgloss.Color("#374151"),
		LatSlow:          lipgloss.Color("#F87171"),
		LatMed:           lipgloss.Color("#FBBF24"),
		LatFast:          lipgloss.Color("#34D399"),
	},
	"mono": {
		Name:             "mono",
		Primary:          lipgloss.Color("#F3F4F6"),
		Success:          lipgloss.Color("#E5E7EB"),
		Warning:          lipgloss.Color("#D1D5DB"),
		Error:            lipgloss.Color("#F9FAFB"),
		Info:             lipgloss.Color("#D1D5DB"),
		Muted:            lipgloss.Color("#9CA3AF"),
		User:             lipgloss.Color("#F3F4F6"),
		Assistant:        lipgloss.Color("#E5E7EB"),
		Tool:             lipgloss.Color("#D1D5DB"),
		RunID:            lipgloss.Color("#F9FAFB"),
		Danger:           lipgloss.Color("#F9FAFB"),
		PaneBorder:       lipgloss.Color("#6B7280"),
		ActivePaneBorder: lipgloss.Color("#F3F4F6"),
		CopyIcon:         lipgloss.Color("#6B7280"),
		LatSlow:          lipgloss.Color("#F9FAFB"),
		LatMed:           lipgloss.Color("#D1D5DB"),
		LatFast:          lipgloss.Color("#E5E7EB"),
	},
	"dracula": {
		Name:             "dracula",
		Primary:          lipgloss.Color("#BD93F9"),
		Success:          lipgloss.Color("#50FA7B"),
		Warning:          lipgloss.Color("#F1FA8C"),
		Error:            lipgloss.Color("#FF5555"),
		Info:             lipgloss.Color("#8BE9FD"),
		Muted:            lipgloss.Color("#6272A4"),
		User:             lipgloss.Color("#FF79C6"),
		Assistant:        lipgloss.Color("#8BE9FD"),
		Tool:             lipgloss.Color("#F1FA8C"),
		RunID:            lipgloss.Color("#BD93F9"),
		Danger:           lipgloss.Color("#FF5555"),
		PaneBorder:       lipgloss.Color("#44475A"),
		ActivePaneBorder: lipgloss.Color("#BD93F9"),
		CopyIcon:         lipgloss.Color("#44475A"),
		LatSlow:          lipgloss.Color("#FF5555"),
		LatMed:           lipgloss.Color("#F1FA8C"),
		LatFast:          lipgloss.Color("#50FA7B"),
	},
	"catppuccin": {
		Name:             "catppuccin",
		Primary:          lipgloss.Color("#CBA6F7"),
		Success:          lipgloss.Color("#A6E3A1"),
		Warning:          lipgloss.Color("#F9E2AF"),
		Error:            lipgloss.Color("#F38BA8"),
		Info:             lipgloss.Color("#89B4FA"),
		Muted:            lipgloss.Color("#6C7086"),
		User:             lipgloss.Color("#F5C2E7"),
		Assistant:        lipgloss.Color("#94E2D5"),
		Tool:             lipgloss.Color("#F9E2AF"),
		RunID:            lipgloss.Color("#B4BEFE"),
		Danger:           lipgloss.Color("#F38BA8"),
		PaneBorder:       lipgloss.Color("#45475A"),
		ActivePaneBorder: lipgloss.Color("#CBA6F7"),
		CopyIcon:         lipgloss.Color("#45475A"),
		LatSlow:          lipgloss.Color("#F38BA8"),
		LatMed:           lipgloss.Color("#F9E2AF"),
		LatFast:          lipgloss.Color("#A6E3A1"),
	},
}

// ThemeV100 is the default v100 palette.
var ThemeV100 = builtinThemes["v100"]

var currentTheme = ThemeV100

// ThemeByName resolves a named built-in theme.
func ThemeByName(name string) (Theme, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || key == "default" {
		key = "v100"
	}
	t, ok := builtinThemes[key]
	return t, ok
}

// ThemeNames returns the sorted list of built-in theme names.
func ThemeNames() []string {
	names := make([]string, 0, len(builtinThemes))
	for name := range builtinThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CurrentTheme returns the currently applied theme.
func CurrentTheme() Theme {
	return currentTheme
}

// ApplyTheme applies a theme and rebuilds package-level derived styles.
func ApplyTheme(t Theme) {
	if strings.TrimSpace(t.Name) == "" {
		t.Name = "custom"
	}
	currentTheme = t
	clrPrimary = t.Primary
	clrSuccess = t.Success
	clrWarning = t.Warning
	clrError = t.Error
	clrInfo = t.Info
	clrMuted = t.Muted
	clrUser = t.User
	clrAssistant = t.Assistant
	clrTool = t.Tool
	clrRunID = t.RunID
	clrDanger = t.Danger
	clrPaneBorder = t.PaneBorder
	clrActivePaneBorder = t.ActivePaneBorder
	clrCopyIcon = t.CopyIcon
	clrLatSlow = t.LatSlow
	clrLatMed = t.LatMed
	clrLatFast = t.LatFast
	rebuildStyles()
}

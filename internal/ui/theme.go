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
	// Gruvbox dark hard/medium palette.
	"gruvbox": {
		Name:             "gruvbox",
		Primary:          lipgloss.Color("#D3869B"),
		Success:          lipgloss.Color("#B8BB26"),
		Warning:          lipgloss.Color("#FABD2F"),
		Error:            lipgloss.Color("#FB4934"),
		Info:             lipgloss.Color("#83A598"),
		Muted:            lipgloss.Color("#928374"),
		User:             lipgloss.Color("#FE8019"),
		Assistant:        lipgloss.Color("#8EC07C"),
		Tool:             lipgloss.Color("#FABD2F"),
		RunID:            lipgloss.Color("#83A598"),
		Danger:           lipgloss.Color("#FB4934"),
		PaneBorder:       lipgloss.Color("#665C54"),
		ActivePaneBorder: lipgloss.Color("#D3869B"),
		CopyIcon:         lipgloss.Color("#7C6F64"),
		LatSlow:          lipgloss.Color("#FB4934"),
		LatMed:           lipgloss.Color("#FABD2F"),
		LatFast:          lipgloss.Color("#B8BB26"),
	},
	// Nord polar night, frost, and aurora palette.
	"nord": {
		Name:             "nord",
		Primary:          lipgloss.Color("#88C0D0"),
		Success:          lipgloss.Color("#A3BE8C"),
		Warning:          lipgloss.Color("#EBCB8B"),
		Error:            lipgloss.Color("#BF616A"),
		Info:             lipgloss.Color("#81A1C1"),
		Muted:            lipgloss.Color("#4C566A"),
		User:             lipgloss.Color("#B48EAD"),
		Assistant:        lipgloss.Color("#8FBCBB"),
		Tool:             lipgloss.Color("#EBCB8B"),
		RunID:            lipgloss.Color("#5E81AC"),
		Danger:           lipgloss.Color("#BF616A"),
		PaneBorder:       lipgloss.Color("#3B4252"),
		ActivePaneBorder: lipgloss.Color("#88C0D0"),
		CopyIcon:         lipgloss.Color("#4C566A"),
		LatSlow:          lipgloss.Color("#BF616A"),
		LatMed:           lipgloss.Color("#EBCB8B"),
		LatFast:          lipgloss.Color("#A3BE8C"),
	},
	// Tokyo Night storm palette.
	"tokyonight": {
		Name:             "tokyonight",
		Primary:          lipgloss.Color("#7AA2F7"),
		Success:          lipgloss.Color("#9ECE6A"),
		Warning:          lipgloss.Color("#E0AF68"),
		Error:            lipgloss.Color("#F7768E"),
		Info:             lipgloss.Color("#7DCFFF"),
		Muted:            lipgloss.Color("#565F89"),
		User:             lipgloss.Color("#BB9AF7"),
		Assistant:        lipgloss.Color("#73DACA"),
		Tool:             lipgloss.Color("#E0AF68"),
		RunID:            lipgloss.Color("#7AA2F7"),
		Danger:           lipgloss.Color("#F7768E"),
		PaneBorder:       lipgloss.Color("#3B4261"),
		ActivePaneBorder: lipgloss.Color("#7AA2F7"),
		CopyIcon:         lipgloss.Color("#565F89"),
		LatSlow:          lipgloss.Color("#F7768E"),
		LatMed:           lipgloss.Color("#E0AF68"),
		LatFast:          lipgloss.Color("#9ECE6A"),
	},
	// Rosé Pine moon palette.
	"rose-pine": {
		Name:             "rose-pine",
		Primary:          lipgloss.Color("#C4A7E7"),
		Success:          lipgloss.Color("#9CCFD8"),
		Warning:          lipgloss.Color("#F6C177"),
		Error:            lipgloss.Color("#EB6F92"),
		Info:             lipgloss.Color("#3E8FB0"),
		Muted:            lipgloss.Color("#6E6A86"),
		User:             lipgloss.Color("#EA9A97"),
		Assistant:        lipgloss.Color("#9CCFD8"),
		Tool:             lipgloss.Color("#F6C177"),
		RunID:            lipgloss.Color("#C4A7E7"),
		Danger:           lipgloss.Color("#EB6F92"),
		PaneBorder:       lipgloss.Color("#26233A"),
		ActivePaneBorder: lipgloss.Color("#C4A7E7"),
		CopyIcon:         lipgloss.Color("#6E6A86"),
		LatSlow:          lipgloss.Color("#EB6F92"),
		LatMed:           lipgloss.Color("#F6C177"),
		LatFast:          lipgloss.Color("#9CCFD8"),
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

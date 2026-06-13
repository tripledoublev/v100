package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestThemeByNameResolvesBuiltIns(t *testing.T) {
	for _, name := range []string{"v100", "mono", "dracula", "catppuccin", "default"} {
		if _, ok := ThemeByName(name); !ok {
			t.Fatalf("ThemeByName(%q) not found", name)
		}
	}
	if _, ok := ThemeByName("missing"); ok {
		t.Fatal("ThemeByName returned ok for missing theme")
	}
}

func TestThemeNamesAreSorted(t *testing.T) {
	names := ThemeNames()
	if strings.Join(names, ",") != "catppuccin,dracula,mono,v100" {
		t.Fatalf("ThemeNames() = %v", names)
	}
}

func TestApplyThemeRebuildsDerivedStyles(t *testing.T) {
	original := CurrentTheme()
	t.Cleanup(func() { ApplyTheme(original) })

	custom := ThemeV100
	custom.Name = "test"
	custom.Primary = lipgloss.Color("#111111")
	custom.ActivePaneBorder = lipgloss.Color("#222222")
	ApplyTheme(custom)

	if CurrentTheme().Name != "test" {
		t.Fatalf("CurrentTheme().Name = %q, want test", CurrentTheme().Name)
	}
	if got := stylePrimary.GetForeground(); got != lipgloss.Color("#111111") {
		t.Fatalf("stylePrimary foreground = %q, want #111111", got)
	}
	if got := tuiActivePaneStyle.GetBorderTopForeground(); got != lipgloss.Color("#222222") {
		t.Fatalf("active pane border = %q, want #222222", got)
	}
}

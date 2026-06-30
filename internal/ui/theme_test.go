package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestThemeByNameResolvesBuiltIns(t *testing.T) {
	for _, name := range []string{"v100", "mono", "dracula", "catppuccin", "gruvbox", "nord", "tokyonight", "rose-pine", "default"} {
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
	if strings.Join(names, ",") != "catppuccin,dracula,gruvbox,mono,nord,rose-pine,tokyonight,v100" {
		t.Fatalf("ThemeNames() = %v", names)
	}
}

func TestBuiltInThemesSetEveryColor(t *testing.T) {
	colorType := reflect.TypeOf(lipgloss.Color(""))
	for name, theme := range builtinThemes {
		value := reflect.ValueOf(theme)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.Type != colorType {
				continue
			}
			if got := value.Field(i).Interface().(lipgloss.Color); string(got) == "" {
				t.Fatalf("theme %q field %s is empty", name, field.Name)
			}
		}
	}
}

func TestApplyThemeRebuildsDerivedStyles(t *testing.T) {
	original := CurrentTheme()
	t.Cleanup(func() { ApplyTheme(original) })

	for name, theme := range builtinThemes {
		ApplyTheme(theme)
		if CurrentTheme().Name != name {
			t.Fatalf("CurrentTheme().Name = %q, want %q", CurrentTheme().Name, name)
		}
	}

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

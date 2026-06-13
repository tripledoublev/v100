package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const userMessageLabel = "●"

// Active color palette.
var (
	clrPrimary          lipgloss.Color
	clrSuccess          lipgloss.Color
	clrWarning          lipgloss.Color
	clrError            lipgloss.Color
	clrInfo             lipgloss.Color
	clrMuted            lipgloss.Color
	clrUser             lipgloss.Color
	clrAssistant        lipgloss.Color
	clrTool             lipgloss.Color
	clrRunID            lipgloss.Color
	clrDanger           lipgloss.Color
	clrPaneBorder       lipgloss.Color
	clrActivePaneBorder lipgloss.Color
	clrCopyIcon         lipgloss.Color
	clrLatSlow          lipgloss.Color
	clrLatMed           lipgloss.Color
	clrLatFast          lipgloss.Color
)

// Named base styles (unexported, used in this package)
var (
	styleOK         lipgloss.Style
	styleFail       lipgloss.Style
	styleWarn       lipgloss.Style
	styleInfo       lipgloss.Style
	styleMuted      lipgloss.Style
	stylePrimary    lipgloss.Style
	styleUser       lipgloss.Style
	styleAssistant  lipgloss.Style
	styleTool       lipgloss.Style
	styleRunID      lipgloss.Style
	styleDanger     lipgloss.Style
	styleBold       lipgloss.Style
	styleLatSlow    lipgloss.Style
	styleLatMed     lipgloss.Style
	styleLatFast    lipgloss.Style
	styleJSONKey    lipgloss.Style
	styleJSONString = lipgloss.NewStyle().Foreground(clrSuccess)
	styleJSONNumber = lipgloss.NewStyle().Foreground(clrWarning)
	styleJSONBool   = lipgloss.NewStyle().Foreground(clrPrimary).Bold(true)
	styleJSONNull   = lipgloss.NewStyle().Foreground(clrMuted).Italic(true)
	styleJSONPunct  = lipgloss.NewStyle().Foreground(clrMuted)
)

// ── Exported helpers used by main.go and cli.go ───────────────────────────────

// OK renders a green ✓ check + message.
func OK(msg string) string { return styleOK.Render("✓") + " " + msg }

// Fail renders a red ✗ + message.
func Fail(msg string) string { return styleFail.Render("✗") + " " + msg }

// Info renders a blue → + message.
func Info(msg string) string { return styleInfo.Render("→") + " " + msg }

// Warn renders an amber ⚠ + message.
func Warn(msg string) string { return styleWarn.Render("⚠") + " " + msg }

// Dim renders muted gray text.
func Dim(msg string) string { return styleMuted.Render(msg) }

// Bold renders bold white text.
func Bold(msg string) string { return styleBold.Render(msg) }

// Primary renders text in the v100 brand violet.
func Primary(msg string) string { return stylePrimary.Render(msg) }

// Header renders a full-width section divider with label.
func Header(label string) string {
	bar := lipgloss.NewStyle().Foreground(clrPrimary).Render("━━━")
	lbl := stylePrimary.Render(label)
	return bar + "  " + lbl
}

// RunBanner renders the startup banner for a new run.
func RunBanner(runID, provider, model string) string {
	bar := lipgloss.NewStyle().Foreground(clrRunID).Render(
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
	)
	label := stylePrimary.Render("v100") +
		styleMuted.Render("  run ") +
		styleRunID.Render(runID[:8]) +
		styleMuted.Render("  "+provider+" · "+model)
	return bar + "\n" + label + "\n" + bar
}

// EndBanner renders the run-end footer.
func EndBanner(reason, runID string, steps, tokens int) string {
	bar := lipgloss.NewStyle().Foreground(clrMuted).Render(
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
	)
	label := styleMuted.Render("run end  ") +
		styleInfo.Render(reason) +
		styleMuted.Render("  steps=") + styleRunID.Render(fmt_int(steps)) +
		styleMuted.Render("  tokens=") + styleRunID.Render(fmt_int(tokens))
	id := styleMuted.Render("run: ") + styleRunID.Render(runID)
	return bar + "\n" + label + "\n" + id
}

func fmt_int(n int) string {
	return lipgloss.NewStyle().Render(itoa(n))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// ── TUI pane / chrome styles ──────────────────────────────────────────────────

var (
	tuiHeaderStyle      lipgloss.Style
	tuiHeaderDimStyle   lipgloss.Style
	tuiPaneStyle        lipgloss.Style
	tuiActivePaneStyle  lipgloss.Style
	tuiInputStyle       lipgloss.Style
	tuiInputActiveStyle lipgloss.Style
	tuiConfirmStyle     lipgloss.Style
	tuiTraceLabelStyle  lipgloss.Style
	tuiStatusLabelStyle lipgloss.Style
	tuiCopyIconStyle    lipgloss.Style
)

func init() {
	ApplyTheme(ThemeV100)
}

func rebuildStyles() {
	styleOK = lipgloss.NewStyle().Foreground(clrSuccess)
	styleFail = lipgloss.NewStyle().Foreground(clrError)
	styleWarn = lipgloss.NewStyle().Foreground(clrWarning)
	styleInfo = lipgloss.NewStyle().Foreground(clrInfo)
	styleMuted = lipgloss.NewStyle().Foreground(clrMuted)
	stylePrimary = lipgloss.NewStyle().Foreground(clrPrimary).Bold(true)
	styleUser = lipgloss.NewStyle().Foreground(clrUser).Bold(true)
	styleAssistant = lipgloss.NewStyle().Foreground(clrAssistant).Bold(true)
	styleTool = lipgloss.NewStyle().Foreground(clrTool).Bold(true)
	styleRunID = lipgloss.NewStyle().Foreground(clrRunID)
	styleDanger = lipgloss.NewStyle().Foreground(clrDanger).Bold(true)
	styleBold = lipgloss.NewStyle().Bold(true)
	styleLatSlow = lipgloss.NewStyle().Foreground(clrLatSlow)
	styleLatMed = lipgloss.NewStyle().Foreground(clrLatMed)
	styleLatFast = lipgloss.NewStyle().Foreground(clrLatFast)
	styleJSONKey = lipgloss.NewStyle().Foreground(clrInfo).Bold(true)
	styleJSONString = lipgloss.NewStyle().Foreground(clrSuccess)
	styleJSONNumber = lipgloss.NewStyle().Foreground(clrWarning)
	styleJSONBool = lipgloss.NewStyle().Foreground(clrPrimary).Bold(true)
	styleJSONNull = lipgloss.NewStyle().Foreground(clrMuted).Italic(true)
	styleJSONPunct = lipgloss.NewStyle().Foreground(clrMuted)

	tuiHeaderStyle = lipgloss.NewStyle().
		Foreground(clrPrimary).
		Bold(true)
	tuiHeaderDimStyle = lipgloss.NewStyle().
		Foreground(clrMuted)
	tuiPaneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrPaneBorder)
	tuiActivePaneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrActivePaneBorder)
	tuiInputStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrPaneBorder)
	tuiInputActiveStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrActivePaneBorder)
	tuiConfirmStyle = lipgloss.NewStyle().
		Bold(true).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(clrDanger).
		Padding(1, 3)
	tuiTraceLabelStyle = lipgloss.NewStyle().
		Foreground(clrMuted).
		Italic(true)
	tuiStatusLabelStyle = lipgloss.NewStyle().
		Foreground(clrMuted).
		Italic(true)
	tuiCopyIconStyle = lipgloss.NewStyle().
		Foreground(clrCopyIcon)
}

// EnablePlainTTY forces monochrome rendering for terminal compatibility.
func EnablePlainTTY() {
	lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetHasDarkBackground(false)
}

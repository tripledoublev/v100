package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const userMessageLabel = "●"

// Color palette
var (
	clrPrimary   = lipgloss.Color("#A78BFA") // violet  — v100 brand
	clrSuccess   = lipgloss.Color("#34D399") // emerald — ok / green checks
	clrWarning   = lipgloss.Color("#FBBF24") // amber   — tools / warnings
	clrError     = lipgloss.Color("#F87171") // red     — errors / fail
	clrInfo      = lipgloss.Color("#60A5FA") // blue    — info / meta
	clrMuted     = lipgloss.Color("#6B7280") // gray    — timestamps / dim text
	clrUser      = lipgloss.Color("#C4B5FD") // light violet — user messages
	clrAssistant = lipgloss.Color("#5EEAD4") // teal    — assistant messages
	clrTool      = lipgloss.Color("#FBBF24") // amber   — tool calls
	clrRunID     = lipgloss.Color("#818CF8") // indigo  — run IDs
	clrDanger    = lipgloss.Color("#F87171") // red     — dangerous tool confirm
	clrLatSlow   = lipgloss.Color("#F87171") // red     — slow model calls (>2s)
	clrLatMed    = lipgloss.Color("#FBBF24") // amber   — medium latency (500ms-2s)
	clrLatFast   = lipgloss.Color("#34D399") // green   — fast model calls (<500ms)
)

// Named base styles (unexported, used in this package)
var (
	styleOK        = lipgloss.NewStyle().Foreground(clrSuccess)
	styleFail      = lipgloss.NewStyle().Foreground(clrError)
	styleWarn      = lipgloss.NewStyle().Foreground(clrWarning)
	styleInfo      = lipgloss.NewStyle().Foreground(clrInfo)
	styleMuted     = lipgloss.NewStyle().Foreground(clrMuted)
	stylePrimary   = lipgloss.NewStyle().Foreground(clrPrimary).Bold(true)
	styleUser      = lipgloss.NewStyle().Foreground(clrUser).Bold(true)
	styleAssistant = lipgloss.NewStyle().Foreground(clrAssistant).Bold(true)
	styleTool      = lipgloss.NewStyle().Foreground(clrTool).Bold(true)
	styleRunID     = lipgloss.NewStyle().Foreground(clrRunID)
	styleDanger    = lipgloss.NewStyle().Foreground(clrDanger).Bold(true)
	styleBold      = lipgloss.NewStyle().Bold(true)
	styleLatSlow   = lipgloss.NewStyle().Foreground(clrLatSlow)
	styleLatMed    = lipgloss.NewStyle().Foreground(clrLatMed)
	styleLatFast   = lipgloss.NewStyle().Foreground(clrLatFast)
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

// EnablePlainTTY forces monochrome rendering for terminal compatibility.
func EnablePlainTTY() {
	lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetHasDarkBackground(false)
}

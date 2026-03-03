package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/core"
)

// CLIRenderer prints events to stdout line by line with colors.
type CLIRenderer struct{}

// NewCLIRenderer creates a CLI renderer.
func NewCLIRenderer() *CLIRenderer {
	return &CLIRenderer{}
}

// RenderEvent prints a human-readable, colorized representation of an event.
func (r *CLIRenderer) RenderEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Format(time.TimeOnly))

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Println(RunBanner(ev.RunID, p.Provider, p.Model))

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			styleUser.Render("you"),
			p.Content,
		)

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			// Indent wrapped lines to align under the label
			indented := indentLines(p.Text, "              ")
			fmt.Printf("\n%s  %s  %s\n",
				ts,
				styleAssistant.Render("v100"),
				indented,
			)
		}
		for _, tc := range p.ToolCalls {
			fmt.Printf("           %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+tc.ArgsJSON+")"),
			)
		}

	case core.EventToolCall:
		// Shown inline in EventModelResp above; skip duplicate output.

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		var icon, statusStyle string
		if p.OK {
			icon = styleOK.Render("✓")
			statusStyle = styleOK.Render(p.Name)
		} else {
			icon = styleFail.Render("✗")
			statusStyle = styleFail.Render(p.Name)
		}
		out := p.Output
		if len(out) > 200 {
			out = out[:200] + "…"
		}
		out = strings.ReplaceAll(out, "\n", " ↵ ")
		fmt.Printf("           %s %s  %s  %s\n",
			icon,
			statusStyle,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			out,
		)

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Fprintf(os.Stderr, "\n%s  %s  %s\n",
			ts,
			styleFail.Render("error"),
			styleFail.Render(p.Error),
		)

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n", EndBanner(p.Reason, p.UsedSteps, p.UsedTokens))

	default:
		fmt.Printf("%s  %s\n", ts, styleMuted.Render(string(ev.Type)))
	}
}

// ConfirmTool prompts the user on stdin to approve a dangerous tool call.
func ConfirmTool(toolName, args string) bool {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrDanger).
		Padding(0, 2).
		Render(
			styleDanger.Render("⚠  DANGEROUS TOOL: "+toolName) + "\n" +
				styleMuted.Render("Args: ") + args + "\n\n" +
				styleWarn.Render("Approve?") + "  " +
				styleOK.Render("[y]") + " yes   " +
				styleFail.Render("[N]") + " no",
		)
	fmt.Printf("\n%s\n\n", box)
	fmt.Print(styleWarn.Render("▸ "))

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// Prompt prints a styled prompt and reads a line from stdin.
func Prompt(prompt string) (string, error) {
	fmt.Print(stylePrimary.Render("▸") + " ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("EOF")
	}
	return scanner.Text(), nil
}

// PrintReplayEvent prints a styled replay view of a trace event.
func PrintReplayEvent(ev core.Event) {
	ts := ev.TS.Format("15:04:05")

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n\n", RunBanner(ev.RunID, p.Provider, p.Model))

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrUser).
			Padding(0, 1).
			Render(
				styleUser.Render("you") + styleMuted.Render("  "+ts) + "\n" +
					p.Content,
			)
		fmt.Printf("\n%s\n", box)

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			meta := styleMuted.Render(fmt.Sprintf("  %s  in=%d out=%d cost=$%.4f",
				ts, p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CostUSD))
			box := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrAssistant).
				Padding(0, 1).
				Render(
					styleAssistant.Render("v100") + meta + "\n" +
						p.Text,
				)
			fmt.Printf("\n%s\n", box)
		}
		for _, tc := range p.ToolCalls {
			fmt.Printf("  %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+tc.ArgsJSON+")"),
			)
		}

	case core.EventToolCall:
		// Covered inline in EventModelResp above.

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		icon := styleOK.Render("✓")
		nameStyle := styleOK.Render(p.Name)
		if !p.OK {
			icon = styleFail.Render("✗")
			nameStyle = styleFail.Render(p.Name)
		}
		out := p.Output
		if len(out) > 500 {
			out = out[:500] + "\n  … (truncated)"
		}
		fmt.Printf("  %s %s %s\n    %s\n",
			icon, nameStyle,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			strings.ReplaceAll(out, "\n", "\n    "),
		)

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s %s\n", styleFail.Render("✗ error:"), styleFail.Render(p.Error))

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n\n", EndBanner(p.Reason, p.UsedSteps, p.UsedTokens))

	default:
		fmt.Printf("%s  %s\n", styleMuted.Render(ts), styleMuted.Render(string(ev.Type)))
	}
}

// indentLines adds a prefix to every line after the first.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

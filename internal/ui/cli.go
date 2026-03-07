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

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "◆ agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "◆ dispatch:" + p.Agent
		}
		fmt.Printf("\n%s  %s  task: %s  model: %s\n",
			ts, styleInfo.Render(label), task, styleMuted.Render(p.Model))

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		pat := ""
		if p.Pattern != "" {
			pat = " " + styleMuted.Render("["+p.Pattern+"]")
		}
		fmt.Printf("\n%s  %s  role=%s%s  task: %s\n",
			ts, styleInfo.Render("◆ dispatch"), p.Agent, pat, task)

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "◆ agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "◆ dispatch:" + p.Agent
		}
		if p.OK {
			fmt.Printf("%s  %s  done  steps=%d tokens=%d cost=$%.4f\n",
				ts, styleOK.Render(label), p.UsedSteps, p.UsedTokens, p.CostUSD)
		} else {
			result := p.Result
			if len(result) > 80 {
				result = result[:80] + "…"
			}
			fmt.Printf("%s  %s  failed: %s\n",
				ts, styleFail.Render(label), result)
		}

	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			styleInfo.Render("⊘ compress"),
			styleMuted.Render(fmt.Sprintf("%d→%d msgs  ~%dk→%dk tok  $%.4f",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000, p.CostUSD)),
		)

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			stylePrimary.Render(fmt.Sprintf("── step %d ──", p.StepNumber)),
			styleMuted.Render(fmt.Sprintf("tok=%dk  $%.4f  %d tools  %d model calls  %dms",
				p.InputTokens/1000, p.CostUSD, p.ToolCalls, p.ModelCalls, p.DurationMS)),
		)

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

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 80 {
			task = task[:80] + "…"
		}
		label := "◆ agent start"
		if strings.TrimSpace(p.Agent) != "" {
			label = "◆ dispatch start (" + p.Agent + ")"
		}
		fmt.Printf("\n  %s  task: %s  model: %s  max_steps: %d\n",
			styleInfo.Render(label), task, styleMuted.Render(p.Model), p.MaxSteps)

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 80 {
			task = task[:80] + "…"
		}
		if p.Pattern != "" {
			fmt.Printf("  %s  role=%s pattern=%s task: %s\n",
				styleInfo.Render("◆ dispatch"), p.Agent, p.Pattern, styleMuted.Render(task))
		} else {
			fmt.Printf("  %s  role=%s task: %s\n",
				styleInfo.Render("◆ dispatch"), p.Agent, styleMuted.Render(task))
		}

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		okLabel := "◆ agent done"
		failLabel := "◆ agent failed"
		if strings.TrimSpace(p.Agent) != "" {
			okLabel = "◆ dispatch done (" + p.Agent + ")"
			failLabel = "◆ dispatch failed (" + p.Agent + ")"
		}
		if p.OK {
			fmt.Printf("  %s  steps=%d tokens=%d cost=$%.4f\n",
				styleOK.Render(okLabel), p.UsedSteps, p.UsedTokens, p.CostUSD)
		} else {
			fmt.Printf("  %s  %s\n",
				styleFail.Render(failLabel), styleFail.Render(p.Result))
		}

	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s  %s\n",
			styleInfo.Render("⊘ compress"),
			styleMuted.Render(fmt.Sprintf("%d→%d msgs  ~%dk→%dk tok  $%.4f",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000, p.CostUSD)),
		)

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s  %s\n",
			stylePrimary.Render(fmt.Sprintf("── step %d ──", p.StepNumber)),
			styleMuted.Render(fmt.Sprintf("tok=%dk  $%.4f  %d tools  %d model calls  %dms",
				p.InputTokens/1000, p.CostUSD, p.ToolCalls, p.ModelCalls, p.DurationMS)),
		)

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

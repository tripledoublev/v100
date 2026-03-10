package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
	"github.com/tripledoublev/v100/internal/core"
)

// CLIRenderer prints events to stdout line by line with colors.
type CLIRenderer struct {
	agentStarts map[string]time.Time // agentRunID → start time
	inSubAgent  int                  // nesting depth of sub-agents
	spinnerStop chan struct{}
	spinnerDone chan struct{}
	Verbose     bool
	mu          sync.Mutex
}

// NewCLIRenderer creates a CLI renderer.
func NewCLIRenderer() *CLIRenderer {
	return &CLIRenderer{
		agentStarts: make(map[string]time.Time),
	}
}

// formatTokens formats token count as "Nk" for ≥1000 tokens, or "N" for <1000.
// Fix #6: Show actual token count when <1k instead of rounding to "0k"
func formatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
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
		label := styleUser.Render("you")
		if p.Source == "system" {
			label = styleWarn.Render("v100")
		}
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			label,
			p.Content,
		)

	case core.EventModelResp:
		if r.inSubAgent > 0 {
			break // suppress sub-agent model responses
		}
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			indented := indentLines(p.Text, "              ")
			fmt.Printf("\n%s  %s  %s\n",
				ts,
				styleAssistant.Render("v100"),
				indented,
			)
		}
		for _, tc := range p.ToolCalls {
			args := TruncateOutput(tc.ArgsJSON, r.Verbose)
			fmt.Printf("           %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+args+")"),
			)
		}

	case core.EventModelToken:
		if r.inSubAgent > 0 {
			break
		}
		var p map[string]string
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Print(p["text"])

	case core.EventToolCallDelta:
		if r.inSubAgent > 0 {
			break
		}
		var p core.ToolCallDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Tool call deltas are hard to render cleanly in line-by-line CLI.
		// We could print a dot or just ignore and wait for the final tool call result.
		// For research fidelity, we'll ignore in CLI but log in trace.

	case core.EventToolOutputDelta:
		if r.inSubAgent > 0 {
			break
		}
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		delta := strings.ReplaceAll(p.Delta, "\n", " ↵ ")
		if len(delta) > 200 {
			delta = delta[:200] + "…"
		}
		fmt.Printf("           %s %s  %s\n",
			styleMuted.Render("↳"),
			styleMuted.Render(p.Stream),
			delta,
		)

	case core.EventToolCall:
		// Suppress sub-agent tool calls; top-level shown inline in EventModelResp.
		break

	case core.EventToolResult:
		if r.inSubAgent > 0 {
			break // suppress sub-agent tool results
		}
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
		out := TruncateOutput(p.Output, r.Verbose)
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
		if p.Summary != "" {
			fmt.Printf("%s\n", styleInfo.Render("Summary: "+p.Summary))
		}

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		r.mu.Lock()
		r.agentStarts[p.AgentRunID] = ev.TS
		r.inSubAgent++
		r.mu.Unlock()
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "Agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "Dispatch:" + p.Agent
		}
		fmt.Printf("\n%s\n", styleInfo.Render(fmt.Sprintf("● %s(%s)", label, task)))
		// Start spinner
		r.mu.Lock()
		r.spinnerStop = make(chan struct{})
		r.spinnerDone = make(chan struct{})
		r.mu.Unlock()
		go r.runSpinner()

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		pat := ""
		if p.Pattern != "" {
			pat = " [" + p.Pattern + "]"
		}
		fmt.Printf("\n%s\n", styleInfo.Render(fmt.Sprintf("● Orchestrate%s %s", pat, task)))

	case core.EventAgentEnd:
		// Stop spinner
		r.mu.Lock()
		if r.spinnerStop != nil {
			close(r.spinnerStop)
			r.spinnerStop = nil
		}
		done := r.spinnerDone
		r.mu.Unlock()
		if done != nil {
			<-done
		}
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		r.mu.Lock()
		startTime := r.agentStarts[p.AgentRunID]
		delete(r.agentStarts, p.AgentRunID)
		r.inSubAgent--
		if r.inSubAgent < 0 {
			r.inSubAgent = 0
		}
		r.mu.Unlock()
		dur := ev.TS.Sub(startTime)
		summary := fmt.Sprintf("%d tool uses · %s · %s",
			p.ToolUses, FormatTokens(p.UsedTokens), FormatDuration(dur.Milliseconds()))
		if p.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", p.CostUSD)
		}
		if p.OK {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleOK.Render("Done")+" "+styleMuted.Render("("+summary+")"))
		} else {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleFail.Render("Failed")+" "+styleMuted.Render("("+summary+")"))
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
			styleMuted.Render(fmt.Sprintf("tok=%s  $%.4f  %d tools  %d model calls  %dms",
				formatTokens(p.InputTokens), p.CostUSD, p.ToolCalls, p.ModelCalls, p.DurationMS)),
		)

	default:
		fmt.Printf("%s  %s\n", ts, styleMuted.Render(string(ev.Type)))
	}
}

// runSpinner displays a cycling spinner until spinnerStop is closed.
func (r *CLIRenderer) runSpinner() {
	r.mu.Lock()
	stop := r.spinnerStop
	done := r.spinnerDone
	r.mu.Unlock()
	defer close(done)

	frames := []string{"-", "\\", "|", "/"}
	i := 0
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Print("\r\033[K") // clear spinner line
			return
		case <-ticker.C:
			fmt.Printf("\r  %s  %s working...", styleMuted.Render("⎿"), styleMuted.Render(frames[i%len(frames)]))
			i++
		}
	}
}

// ConfirmTool prompts the user on stdin to approve a dangerous tool call.
func ConfirmTool(toolName, args string) bool {
	// Fix #2: Detect if stdin is not a TTY (e.g., piped input) and auto-deny
	// to prevent commands like /quit from being misinterpreted as confirmation
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "⚠  Dangerous tool %q denied: stdin is not a terminal (piped/scripted input)\n", toolName)
		return false
	}

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
		borderClr := clrUser
		label := styleUser.Render("you")
		if p.Source == "system" {
			borderClr = clrWarning
			label = styleWarn.Render("v100")
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderClr).
			Padding(0, 1).
			Render(
				label + styleMuted.Render("  "+ts) + "\n" +
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

	case core.EventToolOutputDelta:
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		delta := p.Delta
		if len(delta) > 500 {
			delta = delta[:500] + "\n  … (truncated)"
		}
		fmt.Printf("  %s %s\n    %s\n",
			styleMuted.Render("↳ "+p.Stream),
			styleMuted.Render(p.Name),
			strings.ReplaceAll(delta, "\n", "\n    "),
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
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "Agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "Dispatch:" + p.Agent
		}
		fmt.Printf("\n  %s\n", styleInfo.Render(fmt.Sprintf("● %s(%s)", label, task)))

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		pat := ""
		if p.Pattern != "" {
			pat = " [" + p.Pattern + "]"
		}
		fmt.Printf("  %s\n", styleInfo.Render(fmt.Sprintf("● Orchestrate%s %s", pat, task)))

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		summary := fmt.Sprintf("%d tool uses · %s", p.ToolUses, FormatTokens(p.UsedTokens))
		if p.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", p.CostUSD)
		}
		if p.OK {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleOK.Render("Done")+" "+styleMuted.Render("("+summary+")"))
		} else {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleFail.Render("Failed")+" "+styleMuted.Render("("+summary+")"))
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

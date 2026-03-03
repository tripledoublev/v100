package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

// CLIRenderer prints events to stdout line by line.
type CLIRenderer struct{}

// NewCLIRenderer creates a CLI renderer.
func NewCLIRenderer() *CLIRenderer {
	return &CLIRenderer{}
}

// RenderEvent prints a human-readable representation of an event.
func (r *CLIRenderer) RenderEvent(ev core.Event) {
	ts := ev.TS.Format(time.TimeOnly)
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("[%s] run.start  run=%s provider=%s model=%s policy=%s\n",
			ts, ev.RunID[:8], p.Provider, p.Model, p.Policy)

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("[%s] user       %s\n", ts, p.Content)

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			fmt.Printf("[%s] assistant  %s\n", ts, p.Text)
		}
		for _, tc := range p.ToolCalls {
			fmt.Printf("[%s] tool_call  %s(%s)\n", ts, tc.Name, tc.ArgsJSON)
		}

	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("[%s] tool.call  %s  args=%s\n", ts, p.Name, p.Args)

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "ok"
		if !p.OK {
			status = "FAIL"
		}
		out := p.Output
		if len(out) > 200 {
			out = out[:200] + "..."
		}
		fmt.Printf("[%s] tool.result %s [%s] %dms  %s\n", ts, p.Name, status, p.DurationMS, out)

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Fprintf(os.Stderr, "[%s] ERROR  %s\n", ts, p.Error)

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("[%s] run.end  reason=%s steps=%d tokens=%d\n",
			ts, p.Reason, p.UsedSteps, p.UsedTokens)

	default:
		fmt.Printf("[%s] %s\n", ts, ev.Type)
	}
}

// ConfirmTool prompts the user on stdin to approve a dangerous tool call.
func ConfirmTool(toolName, args string) bool {
	fmt.Printf("\n  DANGEROUS TOOL: %s\n  Args: %s\n  Approve? [y/N] ", toolName, args)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return ans == "y" || ans == "yes"
}

// Prompt prints a prompt and reads a line from stdin.
func Prompt(prompt string) (string, error) {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("EOF")
	}
	return scanner.Text(), nil
}

// PrintReplayEvent prints a replay-friendly view of a trace event.
func PrintReplayEvent(ev core.Event) {
	ts := ev.TS.Format("2006-01-02 15:04:05")
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("━━━ RUN START [%s] run=%s provider=%s model=%s\n",
			ts, ev.RunID, p.Provider, p.Model)

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n┌─ USER [%s]\n│  %s\n└─\n", ts, strings.ReplaceAll(p.Content, "\n", "\n│  "))

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			fmt.Printf("\n┌─ ASSISTANT [%s] tokens=%d+%d cost=$%.4f\n│  %s\n└─\n",
				ts, p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CostUSD,
				strings.ReplaceAll(p.Text, "\n", "\n│  "))
		}
		for _, tc := range p.ToolCalls {
			fmt.Printf("  → TOOL CALL: %s(%s)\n", tc.Name, tc.ArgsJSON)
		}

	case core.EventToolCall:
		// covered by model response

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "✓"
		if !p.OK {
			status = "✗"
		}
		out := p.Output
		if len(out) > 500 {
			out = out[:500] + "\n  ... (truncated)"
		}
		fmt.Printf("  %s RESULT %s [%dms]\n    %s\n", status, p.Name, p.DurationMS,
			strings.ReplaceAll(out, "\n", "\n    "))

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  ✗ ERROR: %s\n", p.Error)

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n━━━ RUN END [%s] reason=%s steps=%d tokens=%d\n",
			ts, p.Reason, p.UsedSteps, p.UsedTokens)

	default:
		fmt.Printf("[%s] %s %s\n", ts, ev.Type, string(ev.Payload))
	}
}

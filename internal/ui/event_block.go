package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"

	"github.com/tripledoublev/v100/internal/core"
)

const (
	traceEventMaxBlockChars  = 1200
	traceEventMaxBlockLines  = 8
	traceEventMaxPayloadList = 4
)

func renderTraceEventBlock(ev *core.Event, width int) []string {
	if ev == nil {
		return []string{"·"}
	}

	title, details := describeTraceEvent(*ev)
	lines := wrapTraceEventText(title, width, "")
	for _, detail := range details {
		if strings.TrimSpace(detail) == "" {
			continue
		}
		lines = append(lines, wrapTraceEventText(detail, width, "  ")...)
	}
	if len(lines) == 0 {
		return []string{string(ev.Type)}
	}
	return lines
}

func describeTraceEvent(ev core.Event) (string, []string) {
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := string(ev.Type)
		if p.Provider != "" || p.Model != "" {
			title += "  " + strings.TrimSpace(p.Provider+" · "+p.Model)
		}
		var details []string
		if p.Policy != "" {
			details = append(details, "policy: "+p.Policy)
		}
		if p.Workspace != "" {
			details = append(details, "workspace: "+p.Workspace)
		}
		return title, details

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := string(ev.Type)
		if p.Source == "system" {
			title += "  system"
		}
		content := strings.TrimSpace(p.Content)
		if p.ImageCount > 0 {
			if content != "" {
				content += " "
			}
			content += imageCount(p.ImageCount)
		}
		if content == "" {
			return title, nil
		}
		return title, []string{content}

	case core.EventModelCall:
		var p core.ModelCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  msgs=%d  tools=%d", ev.Type, len(p.Messages), len(p.ToolNames))
		if p.MaxToolCalls > 0 {
			title += fmt.Sprintf("  max=%d", p.MaxToolCalls)
		}
		var details []string
		if len(p.ToolNames) > 0 {
			details = append(details, "available: "+strings.Join(p.ToolNames, ", "))
		}
		return title, details

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  in=%d  out=%d  cost=$%.4f",
			ev.Type, p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CostUSD)
		if p.DurationMS > 0 {
			title += fmt.Sprintf("  [%dms]", p.DurationMS)
		}
		var details []string
		if text := truncateTraceEventBlock(p.Text); text != "" {
			details = append(details, text)
		}
		for _, tc := range p.ToolCalls {
			details = append(details, "tool: "+tc.Name+"("+truncateTraceEventInline(tc.ArgsJSON, 180)+")")
		}
		return title, details

	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := string(ev.Type)
		if p.Name != "" {
			title += "  " + p.Name
		}
		var details []string
		if strings.TrimSpace(p.Args) != "" {
			details = append(details, "args: "+truncateTraceEventBlock(p.Args))
		}
		return title, details

	case core.EventToolOutputDelta:
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s  %s", ev.Type, p.Name, p.Stream)
		if delta := truncateTraceEventBlock(p.Delta); delta != "" {
			return title, []string{delta}
		}
		return title, nil

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "ok"
		if !p.OK {
			status = "err"
		}
		title := fmt.Sprintf("%s  %s  %s", ev.Type, status, p.Name)
		if p.DurationMS > 0 {
			title += fmt.Sprintf("  [%dms]", p.DurationMS)
		}
		if out := truncateTraceEventBlock(p.Output); out != "" {
			return title, []string{out}
		}
		return title, nil

	case core.EventReflect:
		var p core.ToolReflectPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s  confidence=%.2f", ev.Type, p.Name, p.Confidence)
		if p.Uncertainty != "" {
			return title, []string{p.Uncertainty}
		}
		return title, nil

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Error == "" {
			return string(ev.Type), nil
		}
		return string(ev.Type), []string{p.Error}

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s  steps=%d  tokens=%d",
			ev.Type, p.Reason, p.UsedSteps, p.UsedTokens)
		if p.Summary != "" {
			return title, []string{p.Summary}
		}
		return title, nil

	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s  %d→%d msgs  ~%dk→%dk tok",
			ev.Type, compressEventLabel(p.Trigger),
			p.MessagesBefore, p.MessagesAfter,
			p.TokensBefore/1000, p.TokensAfter/1000)
		if p.Strategy != "" {
			return title, []string{"strategy: " + p.Strategy}
		}
		return title, nil

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  step=%d  tools=%d  model=%d  %dms",
			ev.Type, p.StepNumber, p.ToolCalls, p.ModelCalls, p.DurationMS)
		detail := fmt.Sprintf("input=%d  output=%d  cost=$%.4f",
			p.InputTokens, p.OutputTokens, p.CostUSD)
		return title, []string{detail}

	case core.EventSolverPlan:
		var p map[string]string
		_ = json.Unmarshal(ev.Payload, &p)
		if plan := strings.TrimSpace(p["plan"]); plan != "" {
			return string(ev.Type), []string{plan}
		}
		return string(ev.Type), nil

	case core.EventSolverReplan:
		var p core.SolverReplanPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  attempt=%d", ev.Type, p.Attempt)
		var details []string
		if p.Error != "" {
			details = append(details, "error: "+p.Error)
		}
		if plan := strings.TrimSpace(p.Plan); plan != "" {
			details = append(details, plan)
		}
		return title, details

	case core.EventHookIntervention:
		var p core.HookInterventionPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := string(ev.Type)
		if p.Action != "" {
			title += "  " + p.Action
		}
		var details []string
		if p.Message != "" {
			details = append(details, p.Message)
		}
		if p.Reason != "" {
			details = append(details, "reason: "+p.Reason)
		}
		return title, details

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "dispatch:" + p.Agent
		}
		title := fmt.Sprintf("%s  %s  %s  max=%d",
			ev.Type, label, shortRunID(p.AgentRunID), p.MaxSteps)
		var details []string
		if p.Task != "" {
			details = append(details, p.Task)
		}
		if len(p.Tools) > 0 {
			tools := p.Tools
			if len(tools) > traceEventMaxPayloadList {
				tools = append(append([]string{}, tools[:traceEventMaxPayloadList]...), "...")
			}
			details = append(details, "tools: "+strings.Join(tools, ", "))
		}
		return title, details

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s", ev.Type, shortRunID(p.AgentRunID))
		if p.Pattern != "" {
			title += "  pattern=" + p.Pattern
		}
		if p.Task != "" {
			return title, []string{p.Task}
		}
		return title, nil

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "ok"
		if !p.OK {
			status = "err"
		}
		title := fmt.Sprintf("%s  %s  %s  tools=%d  steps=%d  tok=%d",
			ev.Type, status, shortRunID(p.AgentRunID), p.ToolUses, p.UsedSteps, p.UsedTokens)
		if p.CostUSD > 0 {
			title += fmt.Sprintf("  $%.4f", p.CostUSD)
		}
		if res := truncateTraceEventBlock(p.Result); res != "" {
			return title, []string{res}
		}
		return title, nil

	case core.EventGeneratedGoal:
		var p core.GeneratedGoalPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Content == "" {
			return string(ev.Type), nil
		}
		return string(ev.Type), []string{p.Content}

	case core.EventPolicyEvolve:
		var p core.PolicyEvolvePayload
		_ = json.Unmarshal(ev.Payload, &p)
		title := fmt.Sprintf("%s  %s  baseline=%.3f  candidate=%.3f",
			ev.Type, p.Decision, p.BaselineScore, p.CandidateScore)
		if p.Rationale != "" {
			return title, []string{p.Rationale}
		}
		return title, nil

	default:
		payload := compactJSON(ev.Payload)
		if payload == "" {
			return string(ev.Type), nil
		}
		return string(ev.Type), []string{payload}
	}
}

func formatEventPayloadPretty(ev *core.Event) string {
	if ev == nil {
		return "(no event)"
	}
	payload := bytes.TrimSpace(ev.Payload)
	if len(payload) == 0 {
		return "(no payload)"
	}
	var out bytes.Buffer
	if err := json.Indent(&out, payload, "", "  "); err == nil {
		return out.String()
	}
	return string(payload)
}

func compactJSON(payload []byte) string {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, payload); err == nil {
		return out.String()
	}
	return string(payload)
}

func truncateTraceEventInline(text string, maxRunes int) string {
	text = collapseWhitespace(text)
	return truncateRunes(text, maxRunes)
}

func truncateTraceEventBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	truncated := false
	if len(runes) > traceEventMaxBlockChars {
		text = string(runes[:traceEventMaxBlockChars])
		truncated = true
	}
	lines := strings.Split(text, "\n")
	if len(lines) > traceEventMaxBlockLines {
		lines = lines[:traceEventMaxBlockLines]
		truncated = true
	}
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	text = strings.TrimSpace(strings.Join(lines, "\n"))
	if truncated {
		text = strings.TrimRight(text, "\n") + "\n..."
	}
	return text
}

func wrapTraceEventText(text string, width int, indent string) []string {
	width = max(1, width)
	innerWidth := max(1, width-lipgloss.Width(indent))
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimRight(part, " \t")
		if part == "" {
			lines = append(lines, indent)
			continue
		}
		for _, wrapped := range strings.Split(wrap.String(part, innerWidth), "\n") {
			lines = append(lines, indent+wrapped)
		}
	}
	if len(lines) == 0 {
		return []string{indent}
	}
	return lines
}

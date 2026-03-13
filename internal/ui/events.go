package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/core"
)

func (m *TUIModel) appendEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Format(time.TimeOnly))
	m.updateStatusFromEvent(ev)
	m.lastEventAt = ev.TS
	sub := len(m.activeAgents) > 0

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Initialize metrics metadata
		m.maxSteps = 50 // default fallback
		m.maxTokens = p.ModelMetadata.ContextSize
		if m.maxTokens == 0 {
			m.maxTokens = 100000 // fallback
		}
		if !sub {
			m.transcriptBuf.WriteString(
				stylePrimary.Render("v100") +
					styleMuted.Render("  run "+ev.RunID[:8]+"  "+p.Provider+" · "+p.Model) +
					"\n\n",
			)
			_, _ = fmt.Fprintf(&m.plainBuf, "v100  run %s  %s · %s\n\n", ev.RunID[:8], p.Provider, p.Model)
		}

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			wrapped := m.wrapPlainForTranscript(p.Content)
			label := styleUser.Render("you")
			plainLabel := "you"
			if p.Source == "system" {
				label = styleWarn.Render("v100")
				plainLabel = "v100"
			}
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s  %s\n", ts, label, wrapped)
			iconLine := strings.Count(m.transcriptBuf.String(), "\n")
			m.transcriptBuf.WriteString("           " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
			m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: p.Content})
			_, _ = fmt.Fprintf(&m.plainBuf, "\n%s: %s\n", plainLabel, p.Content)
		}

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.noteModelEvent(ev.TS)
		if sub {
			break // suppress sub-agent model responses from transcript
		}
		m.focus = focusTranscript
		m.input.Blur()
		if p.Text != "" {
			rendered := m.renderMarkdownForPane(p.Text)
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s\n%s\n", ts, styleAssistant.Render("v100"), rendered)
			iconLine := strings.Count(m.transcriptBuf.String(), "\n")
			m.transcriptBuf.WriteString("    " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
			m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: p.Text})
			_, _ = fmt.Fprintf(&m.plainBuf, "\nv100: %s\n", p.Text)
		}
		for _, tc := range p.ToolCalls {
			args := TruncateOutput(tc.ArgsJSON, m.verbose)
			_, _ = fmt.Fprintf(&m.transcriptBuf, "           %s %s%s\n", styleTool.Render("⚙"), styleTool.Render(tc.Name), styleMuted.Render("("+args+")"))
		}

	case core.EventToolCall:
		m.noteToolEvent(ev.TS)
		if sub {
			break // suppress sub-agent tool calls from transcript
		}

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			break // suppress sub-agent tool results from transcript
		}
		icon, nameStr := styleOK.Render("✓"), styleOK.Render(p.Name)
		if !p.OK {
			icon, nameStr = styleFail.Render("✗"), styleFail.Render(p.Name)
		}
		out := SmartSummary(p.Name, p.Output, m.verbose)
		head := fmt.Sprintf("           %s %s  %s", icon, nameStr, styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)))
		body := m.wrapTranscriptBlock(out, "             ")
		_, _ = fmt.Fprintf(&m.transcriptBuf, "%s\n%s\n", head, body)

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s\n", styleMuted.Render(fmt.Sprintf("■ run ended: %s  steps=%d  tokens=%d", p.Reason, p.UsedSteps, p.UsedTokens)))
			if p.Summary != "" {
				_, _ = fmt.Fprintf(&m.transcriptBuf, "%s\n", styleInfo.Render("Summary: "+p.Summary))
			}
			_, _ = fmt.Fprintf(&m.plainBuf, "\n■ run ended: %s  steps=%d  tokens=%d\n", p.Reason, p.UsedSteps, p.UsedTokens)
			if p.Summary != "" {
				_, _ = fmt.Fprintf(&m.plainBuf, "Summary: %s\n", p.Summary)
			}
		}

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			_, _ = fmt.Fprintf(&m.transcriptBuf, "       %s %s\n", styleMuted.Render("◆"), styleFail.Render("error: "+p.Error))
		} else {
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s\n", styleFail.Render("✗ error"), styleFail.Render(p.Error))
			_, _ = fmt.Fprintf(&m.plainBuf, "\nerror: %s\n", p.Error)
		}

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.activeAgents = append(m.activeAgents, agentFrame{
			RunID:    p.AgentRunID,
			CallID:   p.ParentCallID,
			Task:     p.Task,
			Model:    p.Model,
			MaxSteps: p.MaxSteps,
			Tools:    len(p.Tools),
			Started:  ev.TS,
		})
		m.inSubAgent = len(m.activeAgents)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "Agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "Dispatch:" + p.Agent
		}
		_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s\n", styleInfo.Render(fmt.Sprintf("● %s(%s)", label, task)))
		_, _ = fmt.Fprintf(&m.plainBuf, "\n● %s(%s)\n", label, task)

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		var dur time.Duration
		for _, f := range m.activeAgents {
			if f.RunID == p.AgentRunID {
				dur = ev.TS.Sub(f.Started)
				break
			}
		}
		m.removeActiveAgent(p.AgentRunID)
		m.inSubAgent = len(m.activeAgents)
		summary := fmt.Sprintf("%d tool uses · %s · %s",
			p.ToolUses, FormatTokens(p.UsedTokens), FormatDuration(dur.Milliseconds()))
		if p.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", p.CostUSD)
		}
		if p.OK {
			m.agentDoneCount++
			m.lastAgentNote = fmt.Sprintf("done (%s)", summary)
			_, _ = fmt.Fprintf(&m.transcriptBuf, "  %s  %s\n", styleMuted.Render("⎿"), styleOK.Render("Done")+" "+styleMuted.Render("("+summary+")"))
			_, _ = fmt.Fprintf(&m.plainBuf, "  ⎿  Done (%s)\n", summary)
		} else {
			m.agentFailCount++
			m.lastAgentNote = fmt.Sprintf("failed (%s)", summary)
			_, _ = fmt.Fprintf(&m.transcriptBuf, "  %s  %s\n", styleMuted.Render("⎿"), styleFail.Render("Failed")+" "+styleMuted.Render("("+summary+")"))
			_, _ = fmt.Fprintf(&m.plainBuf, "  ⎿  Failed (%s)\n", summary)
		}

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.currentStep = p.StepNumber
		m.inputTokens = p.InputTokens
		m.outputTokens = p.OutputTokens
		m.usedTokens = p.InputTokens + p.OutputTokens
		m.usedCost = p.CostUSD
		m.lastStepMS = p.DurationMS
		m.lastStepTools = p.ToolCalls
	}

	// Trace pane: compact, semantic event stream with per-tool cues.
	m.traceBuf.WriteString(m.renderTraceEvent(ev) + "\n")

	m.transcript.SetContent(m.transcriptBuf.String())
	m.transcript.GotoBottom()
	m.traceView.SetContent(m.traceBuf.String())
	m.traceView.GotoBottom()
}

func (m *TUIModel) renderTraceEvent(ev core.Event) string {
	sep := styleMuted.Render("┊")
	indent := ""
	if m.inSubAgent > 0 && ev.Type != core.EventAgentStart && ev.Type != core.EventAgentEnd {
		indent = styleMuted.Render("· ")
	}

	switch ev.Type {
	case core.EventRunStart:
		return sep + "  " + indent + styleInfo.Render("▶▶") + "  " + styleMuted.Render("run")
	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Source == "system" {
			return sep + "  " + indent + styleWarn.Render(">>") + "  " + styleWarn.Render("v100")
		}
		return sep + "  " + indent + styleUser.Render(">>") + "  " + styleUser.Render("you")
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		latStr := ""
		if p.DurationMS > 0 {
			latStr = "  " + latencyStyle(p.DurationMS).Render(fmt.Sprintf("[%dms]", p.DurationMS))
		}
		if len(p.ToolCalls) > 0 {
			return sep + "  " + indent + styleAssistant.Render("~~") + "  " + styleAssistant.Render(fmt.Sprintf("model  +%d", len(p.ToolCalls))) + latStr
		}
		return sep + "  " + indent + styleAssistant.Render("~~") + "  " + styleAssistant.Render("model") + latStr
	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return sep + "  " + indent + styleWarn.Render(toolGlyph(p.Name)) + "  " + styleWarn.Render(p.Name)
	case core.EventToolOutputDelta:
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		delta := strings.TrimSpace(strings.ReplaceAll(p.Delta, "\n", " "))
		if len(delta) > 48 {
			delta = delta[:48] + "…"
		}
		if delta == "" {
			delta = p.Stream
		}
		return sep + "  " + indent + styleMuted.Render("↳↳") + "  " + styleMuted.Render(
			fmt.Sprintf("%s  %s", p.Stream, delta))
	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		dur := styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS))
		summary := SmartSummary(p.Name, p.Output, m.verbose)
		if p.OK {
			return sep + "  " + indent + styleOK.Render("✓") + "  " + styleOK.Render(p.Name) + "  " + dur + "  " + styleMuted.Render(summary)
		}
		return sep + "  " + indent + styleFail.Render("✗") + "  " + styleFail.Render(p.Name) + "  " + dur + "  " + styleFail.Render("[err] ") + styleMuted.Render(summary)
	case core.EventRunError:
		return sep + "  " + indent + styleFail.Render("!!") + "  " + styleFail.Render("error")
	case core.EventRunEnd:
		return sep + "  " + styleMuted.Render("■■") + "  " + styleMuted.Render("end")
	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "dispatch:" + p.Agent
		}
		return sep + "  " + styleInfo.Render("◆▶") + "  " + styleInfo.Render(
			fmt.Sprintf("%s %s  %s  max=%d", label, shortRunID(p.AgentRunID), p.Model, p.MaxSteps))
	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		label := "agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "dispatch:" + p.Agent
		}
		if p.OK {
			return sep + "  " + styleOK.Render("◆■") + "  " + styleOK.Render(
				fmt.Sprintf("%s %s done  steps=%d tok=%d", label, shortRunID(p.AgentRunID), p.UsedSteps, p.UsedTokens))
		}
		return sep + "  " + styleFail.Render("◆■") + "  " + styleFail.Render(
			fmt.Sprintf("%s %s fail", label, shortRunID(p.AgentRunID)))
	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.traceStepCount = p.StepNumber
		hdr := sep + "  " + stylePrimary.Render(fmt.Sprintf("── step %d ──────────────────────", p.StepNumber))
		detail := sep + "     " + styleMuted.Render(fmt.Sprintf("tok=%dk  $%.4f  %d tools  %dms",
			p.InputTokens/1000, p.CostUSD, p.ToolCalls, p.DurationMS))
		return hdr + "\n" + detail
	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		return sep + "  " + styleInfo.Render("⊘⊘") + "  " + styleInfo.Render(
			fmt.Sprintf("compress  %d→%d msgs  ~%dk→%dk tok",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000))
	default:
		return sep + "  " + indent + styleMuted.Render("::") + "  " + styleMuted.Render(string(ev.Type))
	}
}

func (m *TUIModel) updateStatusFromEvent(ev core.Event) {
	m.statusTick++
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "idle"
		m.statusLine = "booted and listening"
		runShort := ev.RunID
		if len(runShort) > 8 {
			runShort = runShort[:8]
		}
		m.runSummary = fmt.Sprintf("v100 run %s  %s · %s", runShort, p.Provider, p.Model)
	case core.EventUserMsg:
		m.statusMode = "thinking"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"digesting your request",
			"scanning context and constraints",
			"planning a clean approach",
		})
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if len(p.ToolCalls) > 0 {
			m.statusMode = "tooling"
			m.statusLine = pickStatusLine(m.statusTick, []string{
				"looking at code",
				"searching repo",
				"making pancakes",
				"running tools for signal",
			})
		} else {
			m.statusMode = "idle"
			m.statusLine = pickStatusLine(m.statusTick, []string{
				"ready for your next move",
				"response delivered",
				"standing by",
			})
		}
	case core.EventToolCall:
		m.statusMode = "tooling"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"executing tool call",
			"collecting evidence",
			"digging through files",
		})
	case core.EventToolOutputDelta:
		m.statusMode = "tooling"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"streaming tool output",
			"watching command output",
			"tool still running",
		})
	case core.EventToolResult:
		m.statusMode = "thinking"
		m.statusLine = pickStatusLine(m.statusTick, []string{
			"stitching tool outputs together",
			"cross-checking findings",
			"digesting information",
		})
	case core.EventRunError:
		m.statusMode = "error"
		m.statusLine = "hit an error; check transcript"
	case core.EventRunEnd:
		m.statusMode = "idle"
		m.statusLine = "run ended"
	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "tooling"
		m.statusLine = fmt.Sprintf("sub-agent %s running (%s)", shortRunID(p.AgentRunID), p.Model)
	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "thinking"
		if p.OK {
			m.statusLine = fmt.Sprintf("sub-agent %s completed", shortRunID(p.AgentRunID))
		} else {
			m.statusLine = fmt.Sprintf("sub-agent %s failed", shortRunID(p.AgentRunID))
		}
	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.statusMode = "idle"
		m.statusLine = fmt.Sprintf("step %d done — %d tools, %dms, $%.4f",
			p.StepNumber, p.ToolCalls, p.DurationMS, p.CostUSD)
	case core.EventCompress:
		m.noteCompressEvent(ev.TS)
		m.statusMode = "thinking"
		m.statusLine = "context compressed"
	}
}

func (m *TUIModel) noteModelEvent(ts time.Time) {
	m.modelEvents = trimRecentEvents(append(m.modelEvents, ts), ts, 30*time.Second)
}

func (m *TUIModel) noteToolEvent(ts time.Time) {
	m.toolEvents = trimRecentEvents(append(m.toolEvents, ts), ts, 30*time.Second)
}

func (m *TUIModel) noteCompressEvent(ts time.Time) {
	m.compressEvents = trimRecentEvents(append(m.compressEvents, ts), ts, 30*time.Second)
}

func trimRecentEvents(events []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	keep := 0
	for _, ts := range events {
		if !ts.Before(cutoff) {
			events[keep] = ts
			keep++
		}
	}
	return events[:keep]
}

// latencyStyle returns a style colored by latency bracket.
func latencyStyle(ms int64) lipgloss.Style {
	if ms < 500 {
		return styleLatFast
	}
	if ms <= 2000 {
		return styleLatMed
	}
	return styleLatSlow
}

// toolGlyph returns a short, non-emoji Unicode label for a tool name.
func toolGlyph(name string) string {
	switch name {
	case "fs_list":
		return "ls"
	case "fs_read":
		return " <"
	case "fs_write":
		return " >"
	case "fs_mkdir":
		return " +"
	case "project_search":
		return "//"
	case "patch_apply":
		return "~~"
	case "git_status":
		return "g?"
	case "git_diff":
		return "g~"
	case "git_commit":
		return "g+"
	case "git_push":
		return "g^"
	case "curl_fetch":
		return " @"
	case "sh":
		return " $"
	case "agent":
		return " ◆"
	case "dispatch":
		return " ↗"
	case "orchestrate":
		return " ⊕"
	case "blackboard_read":
		return " b<"
	case "blackboard_write":
		return " b>"
	default:
		return "::"
	}
}

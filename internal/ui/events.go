package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/core"
)

func compressEventLabel(trigger string) string {
	switch trigger {
	case "budget_tokens":
		return "compress budget"
	case "context_limit":
		return "compress context"
	default:
		return "compress"
	}
}

func (m *TUIModel) appendEvent(ev core.Event) {
	m.updateStatusFromEvent(ev)
	m.lastEventAt = ev.TS
	sub := len(m.activeAgents) > 0

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.maxSteps = 50
		m.maxTokens = p.ModelMetadata.ContextSize
		if m.maxTokens == 0 {
			m.maxTokens = 100000
		}
		if !sub {
			m.addItem(&TranscriptItem{
				Type:      ItemMessage,
				Role:      "system",
				Text:      fmt.Sprintf("v100  run %s  %s · %s", ev.RunID[:8], p.Provider, p.Model),
				Timestamp: ev.TS,
			})
			_, _ = fmt.Fprintf(&m.plainBuf, "v100  run %s  %s · %s\n\n", ev.RunID[:8], p.Provider, p.Model)
		}

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			role := "user"
			if p.Source == "system" {
				role = "system"
			}
			content := p.Content
			if p.ImageCount > 0 {
				if content != "" {
					content += " "
				}
				content += imageCount(p.ImageCount)
			}
			m.addItem(&TranscriptItem{
				Type:      ItemMessage,
				Role:      role,
				Text:      content,
				Timestamp: ev.TS,
				Images:    nil, // Note: The TUI current event doesn't carry raw bytes for UserMsg attachments yet
			})
			label := userMessageLabel
			if role == "system" {
				label = "v100"
			}
			_, _ = fmt.Fprintf(&m.plainBuf, "\n%s: %s\n", label, content)
		}

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.noteModelEvent(ev.TS)
		if sub {
			break
		}
		if p.Text != "" {
			m.addItem(&TranscriptItem{
				Type:      ItemMessage,
				Role:      "v100",
				Text:      p.Text,
				Timestamp: ev.TS,
			})
			_, _ = fmt.Fprintf(&m.plainBuf, "\nv100: %s\n", p.Text)
		}
		if len(p.ToolCalls) > 0 {
			group := m.getOrCreateToolGroup()
			for _, tc := range p.ToolCalls {
				group.ToolExecs = append(group.ToolExecs, &ToolExecution{
					CallID:    tc.ID,
					Name:      tc.Name,
					Args:      tc.ArgsJSON,
					Timestamp: ev.TS,
				})
			}
		}

	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.noteToolEvent(ev.TS)
		if sub {
			break
		}
		// If this call ID is already in the current group, skip (already added from ModelResp).
		// Otherwise, add it.
		group := m.getOrCreateToolGroup()
		found := false
		for _, ex := range group.ToolExecs {
			if ex.CallID == p.CallID {
				found = true
				break
			}
		}
		if !found {
			group.ToolExecs = append(group.ToolExecs, &ToolExecution{
				CallID:    p.CallID,
				Name:      p.Name,
				Args:      p.Args,
				Timestamp: ev.TS,
			})
		}

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if sub {
			break
		}
		// Find the tool execution in the current group and update it
		group := m.getOrCreateToolGroup()
		for i := len(group.ToolExecs) - 1; i >= 0; i-- {
			if group.ToolExecs[i].CallID == p.CallID {
				group.ToolExecs[i].Result = p.Output
				group.ToolExecs[i].OK = p.OK
				group.ToolExecs[i].Duration = p.DurationMS
				break
			}
		}

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			m.addItem(&TranscriptItem{
				Type:      ItemRunEnd,
				Text:      fmt.Sprintf("■ run ended: %s  steps=%d  tokens=%d\n  run: %s", p.Reason, p.UsedSteps, p.UsedTokens, ev.RunID),
				Timestamp: ev.TS,
			})
			if p.Summary != "" {
				m.addItem(&TranscriptItem{
					Type:      ItemMessage,
					Role:      "system",
					Text:      "Summary: " + p.Summary,
					Timestamp: ev.TS,
				})
			}
			_, _ = fmt.Fprintf(&m.plainBuf, "\n■ run ended: %s  steps=%d  tokens=%d\n", p.Reason, p.UsedSteps, p.UsedTokens)
			if p.Summary != "" {
				_, _ = fmt.Fprintf(&m.plainBuf, "Summary: %s\n", p.Summary)
			}
		}

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			m.addItem(&TranscriptItem{
				Type:      ItemError,
				Text:      p.Error,
				Timestamp: ev.TS,
			})
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
		m.addItem(&TranscriptItem{
			Type:      ItemAgentStart,
			Text:      fmt.Sprintf("● %s(%s)", label, task),
			Timestamp: ev.TS,
		})
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
		if p.OK {
			m.agentDoneCount++
			m.addItem(&TranscriptItem{
				Type:      ItemAgentEnd,
				Text:      "Done (" + summary + ")",
				Expanded:  true, // Always true for AgentEnd
				Timestamp: ev.TS,
			})
			_, _ = fmt.Fprintf(&m.plainBuf, "  ⎿  Done (%s)\n", summary)
		} else {
			m.agentFailCount++
			m.addItem(&TranscriptItem{
				Type:      ItemAgentEnd,
				Text:      "Failed (" + summary + ")",
				Expanded:  false, // Collapsed by default for failure? Or maybe always true.
				Timestamp: ev.TS,
			})
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

	case core.EventImageInline:
		var p core.ImageInlinePayload
		_ = json.Unmarshal(ev.Payload, &p)
		if !sub {
			data, _ := base64.StdEncoding.DecodeString(p.Data)
			if m.imageRenderer != nil {
				_ = m.imageRenderer.Render(data, m.width/2)
			}
			m.addItem(&TranscriptItem{
				Type:      ItemImage,
				Images:    [][]byte{data},
				Timestamp: ev.TS,
			})
		}
	}

	// Trace pane: compact, semantic event stream with per-tool cues.
	m.appendTraceLine(m.renderTraceEvent(ev), ev.Type)

	m.rebuildTranscript(true)
	m.traceView.SetContent(m.traceBuf.String())
	m.traceView.GotoBottom()
}

func (m *TUIModel) addItem(item *TranscriptItem) {
	item.ID = m.nextItemID
	m.nextItemID++
	m.history = append(m.history, item)
}

func (m *TUIModel) getOrCreateToolGroup() *TranscriptItem {
	if len(m.history) > 0 {
		last := m.history[len(m.history)-1]
		if last.Type == ItemToolGroup {
			return last
		}
	}
	item := &TranscriptItem{
		Type:      ItemToolGroup,
		Timestamp: time.Now(),
		Expanded:  false, // Collapsed by default
	}
	m.addItem(item)
	return item
}

func (m *TUIModel) rebuildTranscript(gotoBottom bool) {
	m.transcriptBuf.Reset()
	m.copyTargets = nil
	m.toggleTargets = nil

	for _, item := range m.history {
		ts := styleMuted.Render(item.Timestamp.Local().Format(time.TimeOnly))
		switch item.Type {
		case ItemWelcome:
			m.transcriptBuf.WriteString(stylePrimary.Render("control deck") + styleMuted.Render(" • session ready • "+item.Text) + "\n\n")
			m.transcriptBuf.WriteString(styleBold.Render("Controls") + "\n")
			m.transcriptBuf.WriteString(styleMuted.Render("Enter") + " send  " + styleMuted.Render("Tab") + " focus  " + styleMuted.Render("Ctrl+Shift+Tab") + " half  " + styleMuted.Render("Ctrl+T") + " trace  " + styleMuted.Render("Ctrl+S") + " status  " + styleMuted.Render("Ctrl+C") + " quit\n\n")
			m.transcriptBuf.WriteString(styleMuted.Render("Type a task below and press Enter."))

		case ItemImage:
			if len(item.Images) > 0 {
				img := renderImageSummary(item.Images[0])
				if m.imageRenderer != nil {
					w, h := GetPNGDimensions(item.Images[0])
					img = m.imageRenderer.renderText(w, h, len(item.Images[0]))
				}
				if img != "" {
					// Direct write to buffer without any lipgloss/wrap intervention
					m.transcriptBuf.WriteString("\n\n" + img + "\n\n")
				}
			}

		case ItemMessage:
			switch item.Role {
			case "system":
				if strings.HasPrefix(item.Text, "v100  run") {
					m.transcriptBuf.WriteString(
						stylePrimary.Render("v100") +
							styleMuted.Render("  "+item.Text[6:]) +
							"\n\n",
					)
				} else {
					wrapped := m.wrapPlainForTranscript(item.Text)
					_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s  %s\n", ts, styleWarn.Render("v100"), wrapped)
					iconLine := strings.Count(m.transcriptBuf.String(), "\n")
					m.transcriptBuf.WriteString("           " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
					m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: item.Text})
				}
			case "user":
				wrapped := m.wrapPlainForTranscript(item.Text)
				_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s  %s\n", ts, styleUser.Render(userMessageLabel), wrapped)
				iconLine := strings.Count(m.transcriptBuf.String(), "\n")
				m.transcriptBuf.WriteString("           " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
				m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: item.Text})
			case "v100":
				rendered := m.renderMarkdownForPane(item.Text)
				_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s\n%s\n", ts, styleAssistant.Render("v100"), rendered)
				iconLine := strings.Count(m.transcriptBuf.String(), "\n")
				m.transcriptBuf.WriteString("    " + tuiCopyIconStyle.Render("[⎘ copy]") + "\n")
				m.copyTargets = append(m.copyTargets, copyTarget{lineNo: iconLine, content: item.Text})
			}

		case ItemToolGroup:
			iconLine := strings.Count(m.transcriptBuf.String(), "\n")
			m.toggleTargets = append(m.toggleTargets, toggleTarget{lineNo: iconLine, itemID: item.ID})
			if item.Expanded {
				// Record the toggle line itself for detail
				detailLine := strings.Count(m.transcriptBuf.String(), "\n")
				m.detailTargets = append(m.detailTargets, toolDetailTarget{lineNo: detailLine, exec: nil, groupID: item.ID, toolIndex: -1})

				_, _ = fmt.Fprintf(&m.transcriptBuf, "           %s %s\n", styleTool.Render("[-]"), styleMuted.Render(fmt.Sprintf("%d tool calls", len(item.ToolExecs))))
				for _, exec := range item.ToolExecs {
					args := TruncateOutput(exec.Args, m.verbose)
					_, _ = fmt.Fprintf(&m.transcriptBuf, "           %s %s%s\n", styleTool.Render("⚙"), styleTool.Render(exec.Name), styleMuted.Render("("+args+")"))
					if exec.Result != "" {
						icon, nameStr := styleOK.Render("✓"), styleOK.Render(exec.Name)
						if !exec.OK {
							icon, nameStr = styleFail.Render("✗"), styleFail.Render(exec.Name)
						}
						out := SmartSummary(exec.Name, exec.Result, m.verbose)
						head := fmt.Sprintf("           %s %s  %s", icon, nameStr, styleMuted.Render(fmt.Sprintf("[%dms]", exec.Duration)))
						headLine := strings.Count(m.transcriptBuf.String(), "\n")
						body := m.wrapTranscriptBlock(out, "             ")
						_, _ = fmt.Fprintf(&m.transcriptBuf, "%s\n%s\n", head, body)
						m.detailTargets = append(m.detailTargets, toolDetailTarget{
							lineNo: headLine, exec: exec, groupID: item.ID, toolIndex: -1,
						})
					}
				}
			} else {
				_, _ = fmt.Fprintf(&m.transcriptBuf, "           %s %s\n", styleTool.Render("[+]"), styleMuted.Render(fmt.Sprintf("%d tool calls", len(item.ToolExecs))))
			}

		case ItemAgentStart:
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s\n", styleInfo.Render(item.Text))

		case ItemAgentEnd:
			_, _ = fmt.Fprintf(&m.transcriptBuf, "  %s  %s\n", styleMuted.Render("⎿"), styleInfo.Render(item.Text))

		case ItemRunEnd:
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s\n", styleMuted.Render(item.Text))

		case ItemError:
			_, _ = fmt.Fprintf(&m.transcriptBuf, "\n%s  %s\n", styleFail.Render("✗ error"), styleFail.Render(item.Text))
		}
	}

	m.transcript.SetContent(m.transcriptBuf.String())
	if gotoBottom {
		m.transcript.GotoBottom()
	}
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
		return sep + "  " + indent + styleUser.Render(">>") + "  " + styleUser.Render(userMessageLabel)
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
			fmt.Sprintf("%s  %d→%d msgs  ~%dk→%dk tok",
				compressEventLabel(p.Trigger),
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000))
	case core.EventImageInline:
		return sep + "  " + styleInfo.Render("🖼🖼") + "  " + styleInfo.Render("image inline")
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
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		m.noteCompressEvent(ev.TS)
		m.statusMode = "thinking"
		if p.Trigger == "budget_tokens" {
			m.statusLine = "context compressed to preserve token budget"
		} else {
			m.statusLine = "context compressed"
		}
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
	case "news_fetch":
		return " n"
	case "sh":
		return " $"
	case "wiki":
		return " W"
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

package acp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
)

// NewTranslator returns a core.OutputFn that maps v100 events to ACP notifications.
func NewTranslator(conn *Conn, sessionID string) core.OutputFn {
	return func(ev core.Event) {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "run_status_update",
					Title:     fmt.Sprintf("run started: %s/%s", p.Provider, p.Model),
					Status:    "in_progress",
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventRunEnd:
			var p core.RunEndPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "run_status_update",
					Title:     "run ended: " + p.Reason,
					Status:    acpRunEndStatus(p.Reason),
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventRunError:
			var p core.RunErrorPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "run_error",
					Title:     "run error",
					Status:    "failed",
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventStepSummary:
			var p core.StepSummaryPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "step_summary",
					Title:     fmt.Sprintf("step %d summary", p.StepNumber),
					Status:    "completed",
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventAgentStart:
			var p core.AgentStartPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:       "agent_lifecycle",
					ToolCallID: p.AgentRunID,
					Title:      agentTitle("agent started", p.Agent, p.AgentRunID),
					Status:     "in_progress",
					RawOutput:  rawPayload(ev.Payload),
				})
			}

		case core.EventAgentDispatch:
			var p core.AgentDispatchPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:       "agent_lifecycle",
					ToolCallID: p.AgentRunID,
					Title:      agentTitle("agent dispatched", p.Agent, p.AgentRunID),
					Status:     "pending",
					RawOutput:  rawPayload(ev.Payload),
				})
			}

		case core.EventAgentEnd:
			var p core.AgentEndPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				status := "completed"
				if !p.OK {
					status = "failed"
				}
				sendUpdate(conn, sessionID, Update{
					Type:       "agent_lifecycle",
					ToolCallID: p.AgentRunID,
					Title:      agentTitle("agent ended", p.Agent, p.AgentRunID),
					Status:     status,
					RawOutput:  rawPayload(ev.Payload),
				})
			}

		case core.EventHookIntervention:
			var p core.HookInterventionPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "hook_intervention",
					Title:     "hook intervention: " + p.Action,
					Status:    "completed",
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventSandboxSnapshot:
			var p core.SandboxSnapshotPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:       "sandbox_update",
					ToolCallID: p.CallID,
					Title:      "sandbox snapshot",
					Status:     "completed",
					RawOutput:  rawPayload(ev.Payload),
				})
			}

		case core.EventSandboxRestore:
			var p core.SandboxRestorePayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				sendUpdate(conn, sessionID, Update{
					Type:      "sandbox_update",
					Title:     "sandbox restore",
					Status:    "completed",
					RawOutput: rawPayload(ev.Payload),
				})
			}

		case core.EventToolOutputDelta:
			var p core.ToolOutputDeltaPayload
			if json.Unmarshal(ev.Payload, &p) == nil {
				rawOutput, _ := json.Marshal(struct {
					Stream string `json:"stream"`
					Delta  string `json:"delta"`
				}{Stream: p.Stream, Delta: p.Delta})
				sendUpdate(conn, sessionID, Update{
					Type:       "tool_call_update",
					ToolCallID: p.CallID,
					Status:     "in_progress",
					RawOutput:  json.RawMessage(rawOutput),
				})
			}

		case core.EventModelToken:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				sendUpdate(conn, sessionID, Update{
					Type: "agent_message_chunk",
					Content: &ContentBlock{
						Type: "text",
						Text: p.Text,
					},
				})
			}

		case core.EventSolverPlan:
			var plan string
			if err := json.Unmarshal(ev.Payload, &plan); err == nil {
				sendUpdate(conn, sessionID, Update{
					Type: "agent_thought_chunk",
					Content: &ContentBlock{
						Type: "text",
						Text: plan,
					},
				})
			}

		case core.EventSolverReplan:
			var p core.SolverReplanPayload
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				text := p.Plan
				if text == "" {
					text = p.Error
				}
				sendUpdate(conn, sessionID, Update{
					Type: "agent_thought_chunk",
					Content: &ContentBlock{
						Type: "text",
						Text: text,
					},
				})
			}

		case core.EventToolCall:
			var p core.ToolCallPayload
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				kind := "execute"
				switch p.Name {
				case "fs_read", "fs_list", "fs_outline":
					kind = "read"
				case "fs_write", "fs_mkdir", "patch_apply":
					kind = "edit"
				}

				rawInput := json.RawMessage(p.Args)
				if !json.Valid(rawInput) {
					// Fallback if provider emits invalid JSON for args
					marshaled, _ := json.Marshal(struct {
						Args string `json:"args"`
					}{Args: p.Args})
					rawInput = json.RawMessage(marshaled)
				}

				sendUpdate(conn, sessionID, Update{
					Type:       "tool_call",
					ToolCallID: p.CallID,
					Title:      p.Name,
					Kind:       kind,
					Status:     "pending",
					RawInput:   rawInput,
				})
			}

		case core.EventToolResult:
			var p core.ToolResultPayload
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				status := "completed"
				if !p.OK {
					status = "failed"
				}
				rawOutput := acpToolResultRawOutput(p)

				sendUpdate(conn, sessionID, Update{
					Type:       "tool_call_update",
					ToolCallID: p.CallID,
					Status:     status,
					RawOutput:  json.RawMessage(rawOutput),
				})
			}
		}
	}
}

func acpToolResultRawOutput(p core.ToolResultPayload) json.RawMessage {
	payload := map[string]any{"output": p.Output}
	if len(p.Structured) > 0 && json.Valid(p.Structured) {
		var structured any
		if json.Unmarshal(p.Structured, &structured) == nil {
			payload["structured"] = structured
		}
	}
	rawOutput, _ := json.Marshal(payload)
	return json.RawMessage(rawOutput)
}

func sendUpdate(conn *Conn, sessionID string, update Update) {
	_ = conn.SendNotification(MethodSessionUpdate, SessionUpdateParams{
		SessionID: sessionID,
		Update:    update,
	})
}

func rawPayload(payload json.RawMessage) json.RawMessage {
	if len(payload) > 0 && json.Valid(payload) {
		return append(json.RawMessage(nil), payload...)
	}
	wrapped, _ := json.Marshal(struct {
		Payload string `json:"payload"`
	}{Payload: string(payload)})
	return json.RawMessage(wrapped)
}

func acpRunEndStatus(reason string) string {
	switch reason {
	case "completed", "prompt_exit", "user_exit", "wake_cycle_complete":
		return "completed"
	case "signal_interrupt":
		return "failed"
	default:
		if strings.HasPrefix(reason, "budget_") || reason == "error" || strings.HasSuffix(reason, "_exhausted") {
			return "failed"
		}
		return "completed"
	}
}

func agentTitle(prefix, agent, runID string) string {
	if agent != "" {
		return prefix + ": " + agent
	}
	if runID != "" {
		return prefix + ": " + runID
	}
	return prefix
}

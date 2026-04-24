package acp

import (
	"encoding/json"

	"github.com/tripledoublev/v100/internal/core"
)

// NewTranslator returns a core.OutputFn that maps v100 events to ACP notifications.
func NewTranslator(conn *Conn, sessionID string) core.OutputFn {
	return func(ev core.Event) {
		switch ev.Type {
		case core.EventModelToken:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				_ = conn.SendNotification("session/update", SessionUpdateParams{
					SessionID: sessionID,
					Update: Update{
						Type: "agent_message_chunk",
						Content: &ContentBlock{
							Type: "text",
							Text: p.Text,
						},
					},
				})
			}

		case core.EventSolverPlan:
			var plan string
			if err := json.Unmarshal(ev.Payload, &plan); err == nil {
				_ = conn.SendNotification("session/update", SessionUpdateParams{
					SessionID: sessionID,
					Update: Update{
						Type: "agent_thought_chunk",
						Content: &ContentBlock{
							Type: "text",
							Text: plan,
						},
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
				_ = conn.SendNotification("session/update", SessionUpdateParams{
					SessionID: sessionID,
					Update: Update{
						Type: "agent_thought_chunk",
						Content: &ContentBlock{
							Type: "text",
							Text: text,
						},
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

				_ = conn.SendNotification("session/update", SessionUpdateParams{
					SessionID: sessionID,
					Update: Update{
						Type:       "tool_call",
						ToolCallID: p.CallID,
						Title:      p.Name,
						Kind:       kind,
						Status:     "pending",
						RawInput:   rawInput,
					},
				})
			}

		case core.EventToolResult:
			var p core.ToolResultPayload
			if err := json.Unmarshal(ev.Payload, &p); err == nil {
				status := "completed"
				if !p.OK {
					status = "failed"
				}
				rawOutput, _ := json.Marshal(struct {
					Output string `json:"output"`
				}{Output: p.Output})

				_ = conn.SendNotification("session/update", SessionUpdateParams{
					SessionID: sessionID,
					Update: Update{
						Type:       "tool_call_update",
						ToolCallID: p.CallID,
						Status:     status,
						RawOutput:  json.RawMessage(rawOutput),
					},
				})
			}
		}
	}
}

package core

import (
	"encoding/json"
	"time"
)

// EventType identifies the kind of trace event.
type EventType string

const (
	EventRunStart   EventType = "run.start"
	EventUserMsg    EventType = "user.message"
	EventModelResp  EventType = "model.response"
	EventToolCall   EventType = "tool.call"
	EventToolResult EventType = "tool.result"
	EventRunError   EventType = "run.error"
	EventRunEnd     EventType = "run.end"
)

// Event is a single entry in the trace log.
type Event struct {
	TS      time.Time       `json:"ts"`
	RunID   string          `json:"run_id"`
	StepID  string          `json:"step_id"`
	EventID string          `json:"event_id"`
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ToolCall represents a single tool invocation request from the model.
type ToolCall struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ArgsJSON string `json:"args_json"`
}

// Usage tracks token consumption for a model call.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// ModelOutput is the parsed result from a provider completion call.
type ModelOutput struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls"`
	Usage     Usage      `json:"usage"`
}

// Budget controls resource limits for a run.
type Budget struct {
	MaxSteps    int
	MaxTokens   int
	MaxCostUSD  float64
	UsedSteps   int
	UsedTokens  int
	UsedCostUSD float64
}

// Run holds runtime state for a single agent run.
type Run struct {
	ID        string
	Dir       string // runs/<run_id>/
	TraceFile string // runs/<run_id>/trace.jsonl
	Budget    Budget
}

// RunStartPayload is the Payload for EventRunStart.
type RunStartPayload struct {
	Policy   string `json:"policy"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// UserMsgPayload is the Payload for EventUserMsg.
type UserMsgPayload struct {
	Content string `json:"content"`
}

// ModelRespPayload is the Payload for EventModelResp.
type ModelRespPayload struct {
	Text      string    `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage     `json:"usage"`
}

// ToolCallPayload is the Payload for EventToolCall.
type ToolCallPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Args   string `json:"args"`
}

// ToolResultPayload is the Payload for EventToolResult.
type ToolResultPayload struct {
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
}

// RunErrorPayload is the Payload for EventRunError.
type RunErrorPayload struct {
	Error string `json:"error"`
}

// RunEndPayload is the Payload for EventRunEnd.
type RunEndPayload struct {
	Reason     string `json:"reason"` // "user_exit", "budget_steps", "budget_tokens", "budget_cost", "error"
	UsedSteps  int    `json:"used_steps"`
	UsedTokens int    `json:"used_tokens"`
}

// ErrBudgetExceeded is returned when any budget limit is hit.
type ErrBudgetExceeded struct {
	Reason string
}

func (e *ErrBudgetExceeded) Error() string {
	return "budget exceeded: " + e.Reason
}

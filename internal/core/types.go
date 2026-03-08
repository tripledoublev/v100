package core

import (
	"encoding/json"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// EventType identifies the kind of trace event.
type EventType string

const (
	EventRunStart        EventType = "run.start"
	EventUserMsg         EventType = "user.message"
	EventModelCall       EventType = "model.call"
	EventModelResp       EventType = "model.response"
	EventModelToken      EventType = "model.token"
	EventToolCall        EventType = "tool.call"
	EventToolCallDelta   EventType = "tool.call_delta"
	EventToolOutputDelta EventType = "tool.output_delta"
	EventToolResult      EventType = "tool.result"
	EventToolReflect     EventType = "tool.reflect"
	EventRunError        EventType = "run.error"
	EventRunEnd          EventType = "run.end"
	EventSandboxSnapshot EventType = "sandbox.snapshot"
	EventSandboxRestore  EventType = "sandbox.restore"
	EventAgentStart      EventType = "agent.start"
	EventAgentDispatch   EventType = "agent.dispatch"
	EventAgentEnd        EventType = "agent.end"
	EventCompress        EventType = "context.compress"
	EventStepSummary     EventType = "step.summary"
	EventSolverPlan      EventType = "solver.plan"
	EventSolverReplan    EventType = "solver.replan"
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
	Policy    string `json:"policy"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Workspace string `json:"workspace,omitempty"`
}

// UserMsgPayload is the Payload for EventUserMsg.
type UserMsgPayload struct {
	Content string `json:"content"`
}

// ModelCallPayload is the Payload for EventModelCall.
type ModelCallPayload struct {
	Model        string              `json:"model,omitempty"`
	Messages     []providers.Message `json:"messages"`
	ToolNames    []string            `json:"tool_names,omitempty"`
	MaxToolCalls int                 `json:"max_tool_calls,omitempty"`
}

// ModelRespPayload is the Payload for EventModelResp.
type ModelRespPayload struct {
	Text       string     `json:"text"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Usage      Usage      `json:"usage"`
	DurationMS int64      `json:"duration_ms,omitempty"`
	TTFT       int64      `json:"ttft,omitempty"` // Time To First Token in ms
}

// CompressPayload is the Payload for EventCompress.
type CompressPayload struct {
	MessagesBefore int     `json:"messages_before"`
	MessagesAfter  int     `json:"messages_after"`
	TokensBefore   int     `json:"tokens_before"`
	TokensAfter    int     `json:"tokens_after"`
	CostUSD        float64 `json:"cost_usd"`
}

// StepSummaryPayload is the Payload for EventStepSummary.
type StepSummaryPayload struct {
	StepNumber   int     `json:"step_number"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	ToolCalls    int     `json:"tool_calls"`
	ModelCalls   int     `json:"model_calls"`
	DurationMS   int64   `json:"duration_ms"`
}

// SolverReplanPayload is the payload for EventSolverReplan.
type SolverReplanPayload struct {
	Attempt int    `json:"attempt"`
	Error   string `json:"error,omitempty"`
	Plan    string `json:"plan,omitempty"`
}

// ToolCallPayload is the Payload for EventToolCall.
type ToolCallPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Args   string `json:"args"`
}

// ToolCallDeltaPayload is the Payload for EventToolCallDelta.
type ToolCallDeltaPayload struct {
	CallID string `json:"call_id"`
	Delta  string `json:"delta"`
}

// ToolOutputDeltaPayload is the Payload for EventToolOutputDelta.
type ToolOutputDeltaPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Stream string `json:"stream"`
	Delta  string `json:"delta"`
}

// ToolResultPayload is the Payload for EventToolResult.
type ToolResultPayload struct {
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	Output     string `json:"output"`
	DurationMS int64  `json:"duration_ms"`
}

// ToolReflectPayload is the Payload for EventToolReflect.
type ToolReflectPayload struct {
	CallID      string  `json:"call_id"`
	Name        string  `json:"name"`
	Confidence  float64 `json:"confidence"`
	Uncertainty string  `json:"uncertainty,omitempty"`
}

// SandboxSnapshotPayload is the payload for EventSandboxSnapshot.
type SandboxSnapshotPayload struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Method     string `json:"method"`
	Reason     string `json:"reason,omitempty"`
}

// SandboxRestorePayload is the payload for EventSandboxRestore.
type SandboxRestorePayload struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	Method     string `json:"method"`
	Reason     string `json:"reason,omitempty"`
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

// AgentStartPayload is the Payload for EventAgentStart.
type AgentStartPayload struct {
	Agent        string   `json:"agent,omitempty"`
	ParentCallID string   `json:"parent_call_id"`
	AgentRunID   string   `json:"agent_run_id"`
	Task         string   `json:"task"`
	Model        string   `json:"model"`
	Tools        []string `json:"tools"`
	MaxSteps     int      `json:"max_steps"`
}

// AgentDispatchPayload is the Payload for EventAgentDispatch.
type AgentDispatchPayload struct {
	Agent        string `json:"agent,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	ParentCallID string `json:"parent_call_id"`
	AgentRunID   string `json:"agent_run_id"`
	Task         string `json:"task"`
}

// AgentEndPayload is the Payload for EventAgentEnd.
type AgentEndPayload struct {
	Agent        string  `json:"agent,omitempty"`
	ParentCallID string  `json:"parent_call_id"`
	AgentRunID   string  `json:"agent_run_id"`
	OK           bool    `json:"ok"`
	Result       string  `json:"result"`
	ToolUses     int     `json:"tool_uses"`
	UsedSteps    int     `json:"used_steps"`
	UsedTokens   int     `json:"used_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// ErrBudgetExceeded is returned when any budget limit is hit.
type ErrBudgetExceeded struct {
	Reason string
}

func (e *ErrBudgetExceeded) Error() string {
	return "budget exceeded: " + e.Reason
}

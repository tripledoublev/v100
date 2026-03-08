package providers

import (
	"context"
	"encoding/json"
)

// Capabilities describes what a provider supports.
type Capabilities struct {
	ToolCalls bool
	JSONMode  bool
	Streaming bool
}

// Message is a single entry in the conversation history.
type Message struct {
	Role       string // "system", "user", "assistant", "tool"
	Content    string
	ToolCallID string     // for role=tool results
	Name       string     // for role=tool: tool name
	ToolCalls  []ToolCall // for role=assistant tool-calling turns
}

// ToolSpec describes a tool to be sent to the provider.
type ToolSpec struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
}

// Hints provide request-level tuning hints.
type Hints struct {
	JSONOnly      bool
	ToolCallsOnly bool
	MaxToolCalls  int
}

// GenParams holds generation parameters for controlling model output.
// Pointer fields are optional — nil means use provider default.
type GenParams struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	TopK          *int     `json:"top_k,omitempty"`
	MaxTokens     int      `json:"max_tokens,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
	Seed          *int     `json:"seed,omitempty"`
}

// CompleteRequest is a provider completion request.
type CompleteRequest struct {
	RunID     string
	StepID    string
	Messages  []Message
	Tools     []ToolSpec
	Hints     Hints
	GenParams GenParams
	Model     string
}

// ToolCall is a tool invocation returned by the model.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Usage captures token consumption for a single completion.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// CompleteResponse holds the parsed result from a provider.
type CompleteResponse struct {
	AssistantText string
	ToolCalls     []ToolCall
	Usage         Usage
	Raw           json.RawMessage // raw provider payload (redact secrets before storing)
}

// EmbedRequest is a request to generate an embedding for text.
type EmbedRequest struct {
	Text  string
	Model string
}

// EmbedResponse is the response from an embedding request.
type EmbedResponse struct {
	Embedding []float32
	Usage     Usage
}

// Provider is the interface all LLM backends implement.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error)
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

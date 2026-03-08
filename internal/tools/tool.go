package tools

import (
	"context"
	"encoding/json"

	"github.com/tripledoublev/v100/internal/providers"
)

// DangerLevel classifies how risky a tool operation is.
type DangerLevel string

const (
	Safe      DangerLevel = "safe"
	Dangerous DangerLevel = "dangerous"
)

// ToolCallContext provides runtime context to a tool execution.
type ToolCallContext struct {
	RunID        string
	StepID       string
	CallID       string
	WorkspaceDir string
	TimeoutMS    int
	Provider     providers.Provider
}

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	OK         bool   `json:"ok"`
	Output     string `json:"output"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Tool is the interface all agent tools implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	OutputSchema() json.RawMessage
	DangerLevel() DangerLevel
	Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error)
}

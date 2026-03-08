package tools

import (
	"context"
	"encoding/json"

	"github.com/tripledoublev/v100/internal/core/executor"
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
	WorkspaceDir string // host path to active workspace (sandbox if enabled)
	TimeoutMS    int
	Provider     providers.Provider
	Session      executor.Session // active sandbox session
	Mapper       PathTranslator   // bidirectional path mapping
}

// PathTranslator defines the subset of core.PathMapper needed by tools.
type PathTranslator interface {
	ToSandbox(path string) string
	ToVirtual(path string) string
	SanitizeText(text string) string
	SecurePath(path string) (string, bool)
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

func sanitizeToolResult(call ToolCallContext, result ToolResult) ToolResult {
	if call.Mapper == nil {
		return result
	}
	result.Output = call.Mapper.SanitizeText(result.Output)
	result.Stdout = call.Mapper.SanitizeText(result.Stdout)
	result.Stderr = call.Mapper.SanitizeText(result.Stderr)
	return result
}

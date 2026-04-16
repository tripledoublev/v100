package tools

import (
	"context"
	"encoding/json"
	"io"

	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/providers"
)

// DangerLevel classifies how risky a tool operation is.
type DangerLevel string

const (
	Safe      DangerLevel = "safe"
	Dangerous DangerLevel = "dangerous"
)

// ToolEffects captures execution semantics independent of confirmation risk.
type ToolEffects struct {
	MutatesWorkspace   bool
	MutatesRunState    bool
	NeedsNetwork       bool
	ExternalSideEffect bool
}

// ToolCallContext provides runtime context to a tool execution.
type ToolCallContext struct {
	RunID            string
	StepID           string
	CallID           string
	WorkspaceDir     string // host path to active workspace (sandbox if enabled)
	HostWorkspaceDir string // original source workspace for shared state across runs
	TimeoutMS        int
	Provider         providers.Provider
	EmbedProvider    providers.Provider // dedicated embedding provider; falls back to Provider if nil
	Registry         *Registry          // access to other enabled tools
	Session          executor.Session // active sandbox session
	Mapper           PathTranslator   // bidirectional path mapping
	EmitOutputDelta  func(stream, text string) error
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
	Effects() ToolEffects
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

func outputDeltaWriter(call ToolCallContext, stream string) io.Writer {
	if call.EmitOutputDelta == nil {
		return nil
	}
	return toolOutputDeltaWriter{
		call:   call,
		stream: stream,
	}
}

type toolOutputDeltaWriter struct {
	call   ToolCallContext
	stream string
}

func (w toolOutputDeltaWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	text := string(p)
	if w.call.Mapper != nil {
		text = w.call.Mapper.SanitizeText(text)
	}
	if err := w.call.EmitOutputDelta(w.stream, text); err != nil {
		return 0, err
	}
	return len(p), nil
}

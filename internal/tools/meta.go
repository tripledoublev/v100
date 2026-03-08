package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ─────────────────────────────────────────
// meta.inspect
// ─────────────────────────────────────────

type inspectTool struct{}

func InspectTool() Tool { return &inspectTool{} }

func (t *inspectTool) Name() string { return "inspect_tool" }
func (t *inspectTool) Description() string {
	return "Get the detailed description and input schema of a registered tool to avoid hallucinating parameters."
}
func (t *inspectTool) DangerLevel() DangerLevel { return Safe }
func (t *inspectTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *inspectTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["tool_name"],
		"properties": {
			"tool_name": {"type": "string", "description": "The name of the tool to inspect."}
		}
	}`)
}

func (t *inspectTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"description": {"type": "string"},
			"input_schema": {"type": "object"}
		}
	}`)
}

func (t *inspectTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	if call.Registry == nil {
		return failResult(start, "tool registry not available in this context"), nil
	}

	tool, ok := call.Registry.Get(a.ToolName)
	if !ok {
		return failResult(start, fmt.Sprintf("tool %q not found or not enabled", a.ToolName)), nil
	}

	res := map[string]any{
		"name":         tool.Name(),
		"description":  tool.Description(),
		"input_schema": json.RawMessage(tool.InputSchema()),
	}

	b, err := json.Marshal(res)
	if err != nil {
		return failResult(start, "marshal error: "+err.Error()), nil
	}

	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

//go:build windows
// +build windows

package tools

import (
	"context"
	"encoding/json"
	"time"
)

type fsOutlineTool struct{}

func FSOutline() Tool { return &fsOutlineTool{} }

func (t *fsOutlineTool) Name() string { return "fs_outline" }
func (t *fsOutlineTool) Description() string {
	return "List functions and types in a file using semantic parsing. (Not available on Windows)"
}
func (t *fsOutlineTool) DangerLevel() DangerLevel { return Safe }
func (t *fsOutlineTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *fsOutlineTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "File path to outline."}
		}
	}`)
}

func (t *fsOutlineTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object"}`)
}

func (t *fsOutlineTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	return ToolResult{
		OK:         false,
		Output:     `{"error": "fs_outline is not available on Windows. Please use fs_read and project_search instead."}`,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

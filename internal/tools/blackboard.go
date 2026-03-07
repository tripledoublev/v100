package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type blackboardReadTool struct{}
type blackboardWriteTool struct{}

func BlackboardRead() Tool  { return &blackboardReadTool{} }
func BlackboardWrite() Tool { return &blackboardWriteTool{} }

func (t *blackboardReadTool) Name() string             { return "blackboard_read" }
func (t *blackboardReadTool) Description() string      { return "Read shared run blackboard content." }
func (t *blackboardReadTool) DangerLevel() DangerLevel { return Safe }
func (t *blackboardReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *blackboardReadTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)
}
func (t *blackboardReadTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	path := blackboardPath(call.RunID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult{OK: true, Output: "", DurationMS: time.Since(start).Milliseconds()}, nil
		}
		return failResult(start, "read blackboard: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     string(data),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func (t *blackboardWriteTool) Name() string { return "blackboard_write" }
func (t *blackboardWriteTool) Description() string {
	return "Append or overwrite shared run blackboard content."
}
func (t *blackboardWriteTool) DangerLevel() DangerLevel { return Dangerous }
func (t *blackboardWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"required":["content"],
		"properties":{
			"content":{"type":"string"},
			"append":{"type":"boolean","default":true}
		}
	}`)
}
func (t *blackboardWriteTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"bytes_written":{"type":"integer"}}}`)
}
func (t *blackboardWriteTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Content string `json:"content"`
		Append  *bool  `json:"append"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	appendMode := true
	if a.Append != nil {
		appendMode = *a.Append
	}
	path := blackboardPath(call.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failResult(start, "mkdir: "+err.Error()), nil
	}

	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
		if a.Content != "" && a.Content[len(a.Content)-1] != '\n' {
			a.Content += "\n"
		}
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return failResult(start, "open: "+err.Error()), nil
	}
	defer f.Close()
	n, err := f.WriteString(a.Content)
	if err != nil {
		return failResult(start, "write: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     fmt.Sprintf(`{"bytes_written":%d}`, n),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func blackboardPath(runID string) string {
	return filepath.Join("runs", runID, "blackboard.md")
}

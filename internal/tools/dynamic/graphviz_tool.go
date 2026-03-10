package dynamic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tripledoublev/v100/internal/tools"
)

type graphvizTool struct{}

func Graphviz() tools.Tool { return &graphvizTool{} }

func (t *graphvizTool) Name() string                   { return "graphviz" }
func (t *graphvizTool) Description() string            { return "Renders DOT graph descriptions to PNG images." }
func (t *graphvizTool) DangerLevel() tools.DangerLevel { return tools.Safe }
func (t *graphvizTool) Effects() tools.ToolEffects {
	return tools.ToolEffects{MutatesWorkspace: true}
}

func (t *graphvizTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["dot_string"],
		"properties": {
			"dot_string": {"type": "string", "description": "The DOT graph description string."}
		}
	}`)
}

func (t *graphvizTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"png_path": {"type": "string"},
			"virtual_path": {"type": "string"}
		}
	}`)
}

func (t *graphvizTool) Exec(ctx context.Context, call tools.ToolCallContext, args json.RawMessage) (tools.ToolResult, error) {
	start := time.Now()
	var a struct {
		DotString string `json:"dot_string"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tools.ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}

	// Create output dir in artifacts if it doesn't exist
	artifactsDir := filepath.Join(call.WorkspaceDir, "artifacts")
	_ = os.MkdirAll(artifactsDir, 0755)

	pngFilename := fmt.Sprintf("graph_%d.png", time.Now().UnixNano())
	pngPath := filepath.Join(artifactsDir, pngFilename)

	// Execute dot command via stdin
	cmd := exec.CommandContext(ctx, "dot", "-Tpng", "-o", pngPath)
	cmd.Stdin = bytes.NewReader([]byte(a.DotString))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return tools.ToolResult{OK: false, Output: fmt.Sprintf("dot error: %v, stderr: %s", err, stderr.String())}, nil
	}

	res := map[string]string{
		"png_path":     pngPath,
		"virtual_path": call.Mapper.ToVirtual(pngPath),
	}
	b, _ := json.Marshal(res)

	return tools.ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func init() {
	// Note: In a real v100 dynamic tool flow, the agent might write this file
	// and then we'd have a mechanism to load it. For this test, we just provide the implementation.
}

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// shDenylist contains substring patterns that are always blocked.
var shDenylist = []string{
	"rm -rf /",
	":(){ :|:& };:",
	"dd if=",
	"mkfs",
	"> /dev/sda",
	"chmod -R 777 /",
}

type shTool struct{}

func Sh() Tool { return &shTool{} }

func (t *shTool) Name() string        { return "sh" }
func (t *shTool) Description() string { return "Execute a shell command with a timeout. Use carefully." }
func (t *shTool) DangerLevel() DangerLevel { return Dangerous }

func (t *shTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["cmd"],
		"properties": {
			"cmd":     {"type": "string", "description": "Shell command to run."},
			"timeout": {"type": "integer", "description": "Timeout in milliseconds (default: 30000).", "default": 30000}
		}
	}`)
}

func (t *shTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"stdout":   {"type": "string"},
			"stderr":   {"type": "string"},
			"exit_code": {"type": "integer"}
		}
	}`)
}

func (t *shTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Cmd     string `json:"cmd"`
		Timeout int    `json:"timeout"`
	}
	a.Timeout = 30000
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	// Denylist check
	for _, pattern := range shDenylist {
		if strings.Contains(a.Cmd, pattern) {
			return failResult(start, fmt.Sprintf("command blocked by denylist: %q", pattern)), nil
		}
	}

	timeout := time.Duration(a.Timeout) * time.Millisecond
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", a.Cmd)
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return failResult(start, "exec error: "+err.Error()), nil
		}
	}

	out, _ := json.Marshal(map[string]interface{}{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
	})
	return ToolResult{
		OK:         exitCode == 0,
		Output:     string(out),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

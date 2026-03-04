package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type patchApplyTool struct{}

func PatchApply() Tool { return &patchApplyTool{} }

func (t *patchApplyTool) Name() string        { return "patch_apply" }
func (t *patchApplyTool) Description() string { return "Apply a unified diff patch to files in the workspace." }
func (t *patchApplyTool) DangerLevel() DangerLevel { return Dangerous }

func (t *patchApplyTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["diff"],
		"properties": {
			"diff": {"type": "string", "description": "Unified diff content to apply (output of diff -u or git diff)."},
			"strip": {"type": "integer", "description": "Number of leading path components to strip (-p flag). Default: 1.", "default": 1}
		}
	}`)
}

func (t *patchApplyTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *patchApplyTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Diff  string `json:"diff"`
		Strip *int   `json:"strip"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if a.Diff == "" {
		return failResult(start, "diff is required"), nil
	}

	strip := 1
	if a.Strip != nil {
		strip = *a.Strip
	}

	// Write diff to temp file
	tmpFile, err := os.CreateTemp("", "agent-patch-*.diff")
	if err != nil {
		return failResult(start, "failed to create temp file: "+err.Error()), nil
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(a.Diff); err != nil {
		tmpFile.Close()
		return failResult(start, "failed to write patch: "+err.Error()), nil
	}
	tmpFile.Close()

	timeout := 30 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pArg := fmt.Sprintf("-p%d", strip)
	cmd := exec.CommandContext(ctx, "patch", pArg, "--input", tmpFile.Name(), "--batch")
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	dur := time.Since(start).Milliseconds()
	if err != nil {
		combined := stdout.String() + stderr.String()
		return ToolResult{OK: false, Output: combined, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: dur}, nil
	}
	return ToolResult{OK: true, Output: stdout.String(), Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: dur}, nil
}

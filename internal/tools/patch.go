package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
)

type patchApplyTool struct{}

func PatchApply() Tool { return &patchApplyTool{} }

func (t *patchApplyTool) Name() string { return "patch_apply" }
func (t *patchApplyTool) Description() string {
	return "Apply a unified diff patch to files in the workspace."
}
func (t *patchApplyTool) DangerLevel() DangerLevel { return Dangerous }
func (t *patchApplyTool) Effects() ToolEffects     { return ToolEffects{MutatesWorkspace: true} }

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

	timeout := 30 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pArg := fmt.Sprintf("-p%d", strip)
	if call.Session != nil {
		res, err := call.Session.Run(ctx, executor.RunRequest{
			Command:      "patch",
			Args:         []string{pArg, "--batch"},
			Dir:          ".",
			Stdin:        a.Diff,
			StdoutWriter: outputDeltaWriter(call, "stdout"),
			StderrWriter: outputDeltaWriter(call, "stderr"),
		})
		dur := time.Since(start).Milliseconds()
		if err != nil {
			return sanitizeToolResult(call, ToolResult{OK: false, Output: "exec error: " + err.Error(), DurationMS: dur}), nil
		}
		combined := res.Stdout + res.Stderr
		if res.ExitCode != 0 {
			return sanitizeToolResult(call, ToolResult{OK: false, Output: combined, Stdout: res.Stdout, Stderr: res.Stderr, DurationMS: dur}), nil
		}
		return sanitizeToolResult(call, ToolResult{OK: true, Output: res.Stdout, Stdout: res.Stdout, Stderr: res.Stderr, DurationMS: dur}), nil
	}

	cmd := exec.CommandContext(ctx, "patch", pArg, "--batch")
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}
	cmd.Stdin = bytes.NewBufferString(a.Diff)

	var stdout, stderr bytes.Buffer
	var stdoutW io.Writer = &stdout
	var stderrW io.Writer = &stderr
	if w := outputDeltaWriter(call, "stdout"); w != nil {
		stdoutW = io.MultiWriter(stdoutW, w)
	}
	if w := outputDeltaWriter(call, "stderr"); w != nil {
		stderrW = io.MultiWriter(stderrW, w)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	err := cmd.Run()
	dur := time.Since(start).Milliseconds()
	if err != nil {
		combined := stdout.String() + stderr.String()
		return sanitizeToolResult(call, ToolResult{OK: false, Output: combined, Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: dur}), nil
	}
	return sanitizeToolResult(call, ToolResult{OK: true, Output: stdout.String(), Stdout: stdout.String(), Stderr: stderr.String(), DurationMS: dur}), nil
}

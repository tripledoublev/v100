package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
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
		res, err := runPatchInSession(ctx, call, a.Diff, []string{pArg, "--batch", "--forward"}, false)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			return sanitizeToolResult(call, ToolResult{OK: false, Output: "exec error: " + err.Error(), DurationMS: dur}), nil
		}
		if res.ExitCode != 0 {
			retry, retryErr := runPatchInSession(ctx, call, a.Diff, []string{pArg, "--batch", "--forward", "-l"}, true)
			if retryErr != nil {
				return sanitizeToolResult(call, ToolResult{OK: false, Output: "exec error: " + retryErr.Error(), DurationMS: dur}), nil
			}
			if retry.ExitCode != 0 {
				stdout := combinePatchOutput(res.Stdout, retry.Stdout)
				stderr := combinePatchOutput(res.Stderr, retry.Stderr)
				return sanitizeToolResult(call, ToolResult{
					OK:         false,
					Output:     patchFailureOutput(stdout, stderr),
					Stdout:     stdout,
					Stderr:     stderr,
					DurationMS: dur,
				}), nil
			}
			return sanitizeToolResult(call, ToolResult{
				OK:         true,
				Output:     retry.Stdout,
				Stdout:     retry.Stdout,
				Stderr:     retry.Stderr,
				DurationMS: dur,
			}), nil
		}
		return sanitizeToolResult(call, ToolResult{OK: true, Output: res.Stdout, Stdout: res.Stdout, Stderr: res.Stderr, DurationMS: dur}), nil
	}

	res, err := runPatchOnHost(ctx, call, a.Diff, []string{pArg, "--batch", "--forward"}, false)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return sanitizeToolResult(call, ToolResult{OK: false, Output: "exec error: " + err.Error(), DurationMS: dur}), nil
	}
	if res.ExitCode != 0 {
		retry, retryErr := runPatchOnHost(ctx, call, a.Diff, []string{pArg, "--batch", "--forward", "-l"}, true)
		if retryErr != nil {
			return sanitizeToolResult(call, ToolResult{OK: false, Output: "exec error: " + retryErr.Error(), DurationMS: dur}), nil
		}
		if retry.ExitCode != 0 {
			stdout := combinePatchOutput(res.Stdout, retry.Stdout)
			stderr := combinePatchOutput(res.Stderr, retry.Stderr)
			return sanitizeToolResult(call, ToolResult{
				OK:         false,
				Output:     patchFailureOutput(stdout, stderr),
				Stdout:     stdout,
				Stderr:     stderr,
				DurationMS: dur,
			}), nil
		}
		return sanitizeToolResult(call, ToolResult{
			OK:         true,
			Output:     retry.Stdout,
			Stdout:     retry.Stdout,
			Stderr:     retry.Stderr,
			DurationMS: dur,
		}), nil
	}
	return sanitizeToolResult(call, ToolResult{OK: true, Output: res.Stdout, Stdout: res.Stdout, Stderr: res.Stderr, DurationMS: dur}), nil
}

func runPatchInSession(ctx context.Context, call ToolCallContext, diff string, args []string, emit bool) (executor.Result, error) {
	req := executor.RunRequest{
		Command: "patch",
		Args:    args,
		Dir:     ".",
		Stdin:   diff,
	}
	if emit {
		req.StdoutWriter = outputDeltaWriter(call, "stdout")
		req.StderrWriter = outputDeltaWriter(call, "stderr")
	}
	return call.Session.Run(ctx, req)
}

func runPatchOnHost(ctx context.Context, call ToolCallContext, diff string, args []string, emit bool) (executor.Result, error) {
	cmd := exec.CommandContext(ctx, "patch", args...)
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}
	cmd.Stdin = bytes.NewBufferString(diff)

	var stdout, stderr bytes.Buffer
	var stdoutW io.Writer = &stdout
	var stderrW io.Writer = &stderr
	if emit {
		if w := outputDeltaWriter(call, "stdout"); w != nil {
			stdoutW = io.MultiWriter(stdoutW, w)
		}
		if w := outputDeltaWriter(call, "stderr"); w != nil {
			stderrW = io.MultiWriter(stderrW, w)
		}
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return executor.Result{}, err
		}
	}

	return executor.Result{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      err,
	}, nil
}

func combinePatchOutput(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n")
}

func patchFailureOutput(stdout, stderr string) string {
	combined := combinePatchOutput(stdout, stderr)
	help := "patch_apply failed after retrying with whitespace-tolerant matching. Retry with a smaller unified diff or use fs_write for a full-file rewrite. Avoid ad hoc sed or line-number-based edits."
	if combined == "" {
		return help
	}
	return combined + "\n\n" + help
}

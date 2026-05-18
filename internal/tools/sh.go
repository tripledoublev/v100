package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
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

func (t *shTool) Name() string { return "sh" }
func (t *shTool) Description() string {
	return "Execute a shell command with a timeout. This may read or download external resources and save outputs into the workspace when session/network policy allows it. Commands run with a minimal sanitized environment rather than inheriting the full operator shell env. Use carefully."
}
func (t *shTool) DangerLevel() DangerLevel { return Dangerous }
func (t *shTool) Effects() ToolEffects {
	return ToolEffects{MutatesWorkspace: true, NeedsNetwork: true, ExternalSideEffect: true}
}

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
			"stdout_lines": {"type": "array", "items": {"type": "string"}},
			"stderr":   {"type": "string"},
			"stderr_lines": {"type": "array", "items": {"type": "string"}},
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

	if call.Session == nil {
		return failResult(start, "no active sandbox session available"), nil
	}

	res, err := call.Session.Run(ctx, executor.RunRequest{
		Command:      "sh",
		Args:         []string{"-c", sanitizedShellWrapperScript, "v100-sh", a.Cmd},
		Dir:          ".",
		StdoutWriter: outputDeltaWriter(call, "stdout"),
		StderrWriter: outputDeltaWriter(call, "stderr"),
	})
	if err != nil {
		return failResult(start, "exec error: "+err.Error()), nil
	}

	stdout := res.Stdout
	stderr := res.Stderr
	if call.Mapper != nil {
		stdout = call.Mapper.SanitizeText(stdout)
		stderr = call.Mapper.SanitizeText(stderr)
	}

	out, cappedStdout, cappedStderr, err := marshalShellResult(stdout, stderr, res.ExitCode)
	if err != nil {
		return failResult(start, "marshal result: "+err.Error()), nil
	}
	return ToolResult{
		OK:         res.ExitCode == 0,
		Output:     string(out),
		Stdout:     cappedStdout,
		Stderr:     cappedStderr,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func outputLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func marshalShellResult(stdout, stderr string, exitCode int) ([]byte, string, string, error) {
	streamBudget := DefaultToolResultChars - 2048
	if streamBudget < 1024 {
		streamBudget = DefaultToolResultChars / 2
	}
	stdoutBudget, stderrBudget := splitShellStreamBudget(len(stdout), len(stderr), streamBudget)
	includeLines := len(stdout)+len(stderr) <= 4096

	for {
		cappedStdout := TruncateOutput(stdout, stdoutBudget)
		cappedStderr := TruncateOutput(stderr, stderrBudget)
		payload := map[string]any{
			"stdout":    cappedStdout,
			"stderr":    cappedStderr,
			"exit_code": exitCode,
		}
		if includeLines {
			if lines := outputLines(cappedStdout); len(lines) > 0 {
				payload["stdout_lines"] = lines
			}
			if lines := outputLines(cappedStderr); len(lines) > 0 {
				payload["stderr_lines"] = lines
			}
		}

		out, err := json.Marshal(payload)
		if err != nil {
			return nil, "", "", err
		}
		if len(out) <= DefaultToolResultChars {
			return out, cappedStdout, cappedStderr, nil
		}
		if includeLines {
			includeLines = false
			continue
		}
		if stdoutBudget <= 128 && stderrBudget <= 128 {
			return out, cappedStdout, cappedStderr, nil
		}
		stdoutBudget = shrinkShellStreamBudget(stdoutBudget)
		stderrBudget = shrinkShellStreamBudget(stderrBudget)
	}
}

func splitShellStreamBudget(stdoutLen, stderrLen, budget int) (int, int) {
	if stdoutLen == 0 {
		return 0, budget
	}
	if stderrLen == 0 {
		return budget, 0
	}
	return budget / 2, budget - budget/2
}

func shrinkShellStreamBudget(budget int) int {
	if budget <= 128 {
		return budget
	}
	next := budget / 2
	if next < 128 {
		return 128
	}
	return next
}

const sanitizedShellWrapperScript = `
exec env -i \
PATH="${PATH:-/usr/bin:/bin}" \
HOME="$PWD" \
TMPDIR="${TMPDIR:-/tmp}" \
PWD="$PWD" \
LANG="${LANG:-C.UTF-8}" \
TERM="${TERM:-dumb}" \
SHELL=sh \
sh -c "$1"
`

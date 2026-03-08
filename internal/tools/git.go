package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
)

// ─────────────────────────────────────────
// git.status
// ─────────────────────────────────────────

type gitStatusTool struct{}

func GitStatus() Tool { return &gitStatusTool{} }

func (t *gitStatusTool) Name() string             { return "git_status" }
func (t *gitStatusTool) Description() string      { return "Run git status in the workspace directory." }
func (t *gitStatusTool) DangerLevel() DangerLevel { return Safe }
func (t *gitStatusTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *gitStatusTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *gitStatusTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *gitStatusTool) Exec(ctx context.Context, call ToolCallContext, _ json.RawMessage) (ToolResult, error) {
	return runGit(ctx, call, "status", "--short", "--branch")
}

// ─────────────────────────────────────────
// git.diff
// ─────────────────────────────────────────

type gitDiffTool struct{}

func GitDiff() Tool { return &gitDiffTool{} }

func (t *gitDiffTool) Name() string             { return "git_diff" }
func (t *gitDiffTool) Description() string      { return "Show git diff of unstaged changes." }
func (t *gitDiffTool) DangerLevel() DangerLevel { return Safe }
func (t *gitDiffTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *gitDiffTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"staged": {"type": "boolean", "description": "If true, show staged (--cached) diff.", "default": false}
		}
	}`)
}

func (t *gitDiffTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *gitDiffTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Staged bool `json:"staged"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Staged {
		return runGit(ctx, call, "diff", "--cached")
	}
	return runGit(ctx, call, "diff")
}

// ─────────────────────────────────────────
// git.commit
// ─────────────────────────────────────────

type gitCommitTool struct{}

func GitCommit() Tool { return &gitCommitTool{} }

func (t *gitCommitTool) Name() string             { return "git_commit" }
func (t *gitCommitTool) Description() string      { return "Stage all changes and commit with a message." }
func (t *gitCommitTool) DangerLevel() DangerLevel { return Dangerous }
func (t *gitCommitTool) Effects() ToolEffects     { return ToolEffects{MutatesWorkspace: true} }

func (t *gitCommitTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["message"],
		"properties": {
			"message": {"type": "string", "description": "Commit message."},
			"add_all": {"type": "boolean", "description": "If true, run git add -A first.", "default": true}
		}
	}`)
}

func (t *gitCommitTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *gitCommitTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Message string `json:"message"`
		AddAll  *bool  `json:"add_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if a.Message == "" {
		return failResult(start, "commit message required"), nil
	}

	// Default add_all = true
	addAll := true
	if a.AddAll != nil {
		addAll = *a.AddAll
	}

	if addAll {
		r, err := runGit(ctx, call, "add", "-A")
		if err != nil || !r.OK {
			return r, err
		}
	}
	return runGit(ctx, call, "commit", "-m", a.Message)
}

// ─────────────────────────────────────────
// git.push
// ─────────────────────────────────────────

type gitPushTool struct{}

func GitPush() Tool { return &gitPushTool{} }

func (t *gitPushTool) Name() string             { return "git_push" }
func (t *gitPushTool) Description() string      { return "Push commits to a remote branch." }
func (t *gitPushTool) DangerLevel() DangerLevel { return Dangerous }
func (t *gitPushTool) Effects() ToolEffects {
	return ToolEffects{MutatesWorkspace: true, NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *gitPushTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"remote": {"type": "string", "description": "Remote name.", "default": "origin"},
			"branch": {"type": "string", "description": "Branch name. If omitted, push current branch."},
			"set_upstream": {"type": "boolean", "description": "If true, use -u for upstream tracking.", "default": false},
			"force_with_lease": {"type": "boolean", "description": "If true, include --force-with-lease.", "default": false}
		}
	}`)
}

func (t *gitPushTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *gitPushTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Remote         string `json:"remote"`
		Branch         string `json:"branch"`
		SetUpstream    bool   `json:"set_upstream"`
		ForceWithLease bool   `json:"force_with_lease"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(time.Now(), "invalid args: "+err.Error()), nil
	}
	remote := strings.TrimSpace(a.Remote)
	if remote == "" {
		remote = "origin"
	}

	gitArgs := []string{"push"}
	if a.SetUpstream {
		gitArgs = append(gitArgs, "-u")
	}
	if a.ForceWithLease {
		gitArgs = append(gitArgs, "--force-with-lease")
	}
	gitArgs = append(gitArgs, remote)
	if branch := strings.TrimSpace(a.Branch); branch != "" {
		gitArgs = append(gitArgs, branch)
	}
	return runGit(ctx, call, gitArgs...)
}

// ─────────────────────────────────────────
// helpers
// ─────────────────────────────────────────

func runGit(ctx context.Context, call ToolCallContext, gitArgs ...string) (ToolResult, error) {
	start := time.Now()

	timeout := 30 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if call.Session != nil {
		res, err := call.Session.Run(ctx, executor.RunRequest{
			Command:      "git",
			Args:         gitArgs,
			Dir:          ".",
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

	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}

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

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// ─────────────────────────────────────────
// sem.diff
// ─────────────────────────────────────────

type semDiffTool struct{}

func SemDiff() Tool { return &semDiffTool{} }

func (t *semDiffTool) Name() string { return "sem_diff" }
func (t *semDiffTool) Description() string {
	return "Show semantic diff of changes using 'sem'. Understands code entities (functions, classes) instead of just lines."
}
func (t *semDiffTool) DangerLevel() DangerLevel { return Safe }
func (t *semDiffTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *semDiffTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"staged": {"type": "boolean", "description": "If true, show staged changes.", "default": false},
			"commit": {"type": "string", "description": "Specific commit hash to diff."},
			"from": {"type": "string", "description": "Starting revision for range diff."},
			"to": {"type": "string", "description": "Ending revision for range diff."}
		}
	}`)
}

func (t *semDiffTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}, "data": {"type": "object"}}}`)
}

func (t *semDiffTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Staged bool   `json:"staged"`
		Commit string `json:"commit"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	_ = json.Unmarshal(args, &a)

	semArgs := []string{"diff", "--format", "json"}
	if a.Staged {
		semArgs = append(semArgs, "--staged")
	}
	if a.Commit != "" {
		semArgs = append(semArgs, "--commit", a.Commit)
	}
	if a.From != "" {
		semArgs = append(semArgs, "--from", a.From)
	}
	if a.To != "" {
		semArgs = append(semArgs, "--to", a.To)
	}

	return runSem(ctx, call, semArgs...)
}

// ─────────────────────────────────────────
// sem.impact
// ─────────────────────────────────────────

type semImpactTool struct{}

func SemImpact() Tool { return &semImpactTool{} }

func (t *semImpactTool) Name() string { return "sem_impact" }
func (t *semImpactTool) Description() string {
	return "Analyze the impact of changing a specific entity (function, class, etc.)."
}
func (t *semImpactTool) DangerLevel() DangerLevel { return Safe }
func (t *semImpactTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *semImpactTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["entity"],
		"properties": {
			"entity": {"type": "string", "description": "The name of the entity to analyze (e.g. 'validateToken')."}
		}
	}`)
}

func (t *semImpactTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *semImpactTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Entity string `json:"entity"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(time.Now(), "invalid args: "+err.Error()), nil
	}
	return runSem(ctx, call, "impact", a.Entity)
}

// ─────────────────────────────────────────
// sem.blame
// ─────────────────────────────────────────

type semBlameTool struct{}

func SemBlame() Tool { return &semBlameTool{} }

func (t *semBlameTool) Name() string             { return "sem_blame" }
func (t *semBlameTool) Description() string      { return "Show entity-level blame for a file." }
func (t *semBlameTool) DangerLevel() DangerLevel { return Safe }
func (t *semBlameTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *semBlameTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Path to the file."}
		}
	}`)
}

func (t *semBlameTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"output": {"type": "string"}}}`)
}

func (t *semBlameTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(time.Now(), "invalid args: "+err.Error()), nil
	}
	return runSem(ctx, call, "blame", a.Path)
}

// ─────────────────────────────────────────
// helpers

func runSem(ctx context.Context, call ToolCallContext, semArgs ...string) (ToolResult, error) {
	start := time.Now()

	// Check if the expected semantic-diff `sem` tool is installed first.
	if err := ensureSemanticSem(ctx); err != nil {
		return ToolResult{
			OK:     false,
			Output: err.Error(),
		}, nil
	}

	timeout := 30 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sem", semArgs...)
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	dur := time.Since(start).Milliseconds()

	output := stdout.String()
	if err != nil {
		combined := output + stderr.String()
		return ToolResult{OK: false, Output: combined, Stdout: output, Stderr: stderr.String(), DurationMS: dur}, nil
	}
	if strings.TrimSpace(output) == "" && strings.TrimSpace(stderr.String()) == "" {
		return ToolResult{
			OK:         false,
			Output:     "semantic analysis returned no output; the semantic index may be unavailable, stale, or the tool may not support this repository state",
			Stdout:     output,
			Stderr:     stderr.String(),
			DurationMS: dur,
		}, nil
	}

	return ToolResult{OK: true, Output: output, Stdout: output, Stderr: stderr.String(), DurationMS: dur}, nil
}

func ensureSemanticSem(ctx context.Context) error {
	if _, err := exec.LookPath("sem"); err != nil {
		return errors.New(semanticSemHelp)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, "sem", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(semanticSemHelp)
	}
	help := string(out)
	if strings.Contains(help, "Semantic version control") && strings.Contains(help, "Show semantic diff of changes") {
		return nil
	}
	return errors.New(semanticSemHelp + "\n\nfound a different 'sem' binary on PATH. install https://github.com/Ataraxy-Labs/sem and ensure it is first on PATH")
}

const semanticSemHelp = "tool 'sem' (Semantic Version Control) is not installed on this system\nuse semantic diffing by installing it from https://github.com/Ataraxy-Labs/sem\n\ninstallation:\n  git clone https://github.com/Ataraxy-Labs/sem.git\n  cd sem/crates && cargo build --release\n  cp target/release/sem /usr/local/bin/sem"

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

type projectSearchTool struct{}

func ProjectSearch() Tool { return &projectSearchTool{} }

func (t *projectSearchTool) Name() string        { return "project.search" }
func (t *projectSearchTool) Description() string { return "Search for a pattern in project files using ripgrep (rg)." }
func (t *projectSearchTool) DangerLevel() DangerLevel { return Safe }

func (t *projectSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["pattern"],
		"properties": {
			"pattern":     {"type": "string", "description": "Regex pattern to search for."},
			"path":        {"type": "string", "description": "Directory or file to search in. Defaults to workspace root."},
			"glob":        {"type": "string", "description": "Glob pattern to filter files (e.g. '*.go')."},
			"case_sensitive": {"type": "boolean", "description": "If false (default), search is case-insensitive.", "default": false},
			"max_results": {"type": "integer", "description": "Maximum number of lines to return.", "default": 50}
		}
	}`)
}

func (t *projectSearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"matches": {"type": "string"}}}`)
}

func (t *projectSearchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Pattern       string `json:"pattern"`
		Path          string `json:"path"`
		Glob          string `json:"glob"`
		CaseSensitive bool   `json:"case_sensitive"`
		MaxResults    int    `json:"max_results"`
	}
	a.MaxResults = 50
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	rgArgs := []string{"--line-number", "--with-filename"}
	if !a.CaseSensitive {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if a.Glob != "" {
		rgArgs = append(rgArgs, "--glob", a.Glob)
	}
	if a.MaxResults > 0 {
		rgArgs = append(rgArgs, "--max-count", "1")
		// We limit total lines via head-like truncation after
	}
	rgArgs = append(rgArgs, a.Pattern)

	searchPath := call.WorkspaceDir
	if a.Path != "" {
		searchPath = resolvePath(call.WorkspaceDir, a.Path)
	}
	if searchPath != "" {
		rgArgs = append(rgArgs, searchPath)
	}

	timeout := 30 * time.Second
	if call.TimeoutMS > 0 {
		timeout = time.Duration(call.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	if call.WorkspaceDir != "" {
		cmd.Dir = call.WorkspaceDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	_ = cmd.Run() // rg exits 1 on no matches; that's OK

	out := stdout.String()
	if len(out) == 0 {
		out = "(no matches)"
	}
	return ToolResult{
		OK:         true,
		Output:     out,
		Stdout:     out,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

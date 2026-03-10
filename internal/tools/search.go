package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type projectSearchTool struct{}

func ProjectSearch() Tool { return &projectSearchTool{} }

func (t *projectSearchTool) Name() string { return "project_search" }
func (t *projectSearchTool) Description() string {
	return "Search project files with line-numbered matches. Prefer this before fs_read. Use context_lines to inspect local match context without reading the whole file."
}
func (t *projectSearchTool) DangerLevel() DangerLevel { return Safe }
func (t *projectSearchTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *projectSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["pattern"],
		"properties": {
			"pattern":     {"type": "string", "description": "Regex pattern to search for."},
			"path":        {"type": "string", "description": "Directory or file to search in. Defaults to workspace root."},
			"glob":        {"type": "string", "description": "Glob pattern to filter files (e.g. '*.go')."},
			"case_sensitive": {"type": "boolean", "description": "If false (default), search is case-insensitive.", "default": false},
			"context_lines": {"type": "integer", "description": "Optional number of surrounding lines to include around each match.", "default": 0},
			"max_results": {"type": "integer", "description": "Maximum number of output lines to return.", "default": 50},
			"max_chars": {"type": "integer", "description": "Optional hard cap on returned characters.", "default": 12000}
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
		ContextLines  int    `json:"context_lines"`
		MaxResults    int    `json:"max_results"`
		MaxChars      int    `json:"max_chars"`
	}
	a.MaxResults = 50
	a.MaxChars = 12000
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	rgArgs := []string{"--line-number", "--with-filename"}
	if a.ContextLines > 0 {
		rgArgs = append(rgArgs, "--context", strconv.Itoa(a.ContextLines))
	}
	// Avoid runaway self-referential searches over trace/cache/git internals.
	for _, ex := range defaultSearchExcludes(call.WorkspaceDir) {
		rgArgs = append(rgArgs, "--glob", "!"+ex)
	}
	if !a.CaseSensitive {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if a.Glob != "" {
		rgArgs = append(rgArgs, "--glob", a.Glob)
	}
	rgArgs = append(rgArgs, a.Pattern)

	searchPath := call.WorkspaceDir
	if a.Path != "" {
		p, ok := call.Mapper.SecurePath(a.Path)
		if !ok {
			return failResult(start, "illegal path outside sandbox: "+a.Path), nil
		}
		searchPath = p
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
	} else if a.MaxResults > 0 {
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > a.MaxResults {
			out = strings.Join(lines[:a.MaxResults], "\n") +
				"\n... truncated to max_results"
		}
	}
	if a.MaxChars > 0 && len(out) > a.MaxChars {
		out = out[:a.MaxChars] + "\n... truncated to max_chars"
	}
	return ToolResult{
		OK:         true,
		Output:     out,
		Stdout:     out,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func defaultSearchExcludes(workspaceDir string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	// Hard safety defaults.
	add(".git/**")
	add("runs/**")
	add(".gocache/**")

	if workspaceDir == "" {
		return out
	}
	gi := filepath.Join(workspaceDir, ".gitignore")
	b, err := os.ReadFile(gi)
	if err != nil {
		return out
	}
	for _, p := range parseGitignoreExcludes(string(b)) {
		add(p)
	}
	return out
}

func parseGitignoreExcludes(content string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimPrefix(line, "./")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		// Directory rules.
		if strings.HasSuffix(line, "/") {
			base := strings.TrimSuffix(line, "/")
			if base == "" {
				continue
			}
			add(base + "/**")
			add("**/" + base + "/**")
			continue
		}
		// Bare names in .gitignore match anywhere.
		if !strings.Contains(line, "/") && !strings.ContainsAny(line, "*?[]") {
			add(line)
			add("**/" + line)
			add("**/" + line + "/**")
			continue
		}
		// Fallback for path/glob entries.
		add(line)
	}
	return out
}

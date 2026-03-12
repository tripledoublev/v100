package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─────────────────────────────────────────
// fs.read
// ─────────────────────────────────────────

type fsReadTool struct{}

func FSRead() Tool { return &fsReadTool{} }

func (t *fsReadTool) Name() string { return "fs_read" }
func (t *fsReadTool) Description() string {
	return "Read a file, preferably using line ranges for targeted inspection after project_search hits."
}
func (t *fsReadTool) DangerLevel() DangerLevel { return Safe }
func (t *fsReadTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *fsReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Absolute or relative file path to read."},
			"start_line": {"type": "integer", "description": "Optional 1-based starting line for a targeted read."},
			"end_line": {"type": "integer", "description": "Optional 1-based ending line for a targeted read."},
			"max_chars": {"type": "integer", "description": "Optional hard cap on returned characters.", "default": 12000}
		}
	}`)
}

func (t *fsReadTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"content": {"type": "string"}}}`)
}

func (t *fsReadTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
		MaxChars  int    `json:"max_chars"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	path, ok := call.Mapper.SecurePath(a.Path)
	if !ok {
		return failResult(start, "illegal path outside sandbox: "+a.Path), nil
	}
	if looksLikeBinary(path) {
		return failResult(start, fmt.Sprintf("fs_read: %q appears to be a binary file — reading it would produce noise. Use project_search or fs_list to explore the workspace instead.", filepath.Base(path))), nil
	}
	content, err := readFileSelection(path, a.StartLine, a.EndLine)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	if a.MaxChars <= 0 {
		a.MaxChars = 12000
	}
	if a.MaxChars > 0 && len(content) > a.MaxChars {
		content = content[:a.MaxChars] + "\n... truncated to max_chars"
	}
	return ToolResult{
		OK:         true,
		Output:     content,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// isLowSalienceEntry returns true for filenames that are generated artifacts
// agents should not read during workspace exploration.
func isLowSalienceEntry(name string) bool {
	// Known compiled binary names
	switch name {
	case "v100", "v100.exe":
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".tar", ".gz", ".zip", ".so", ".a", ".o", ".dylib", ".exe", ".wasm":
		return true
	}
	return false
}

// looksLikeBinary returns true if the file is likely a compiled binary or
// archive that would produce noise if read as text.
func looksLikeBinary(path string) bool {
	// Extension-based fast path
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".tar", ".gz", ".zip", ".so", ".a", ".o", ".dylib", ".exe", ".wasm":
		return true
	}
	// No extension — peek at first 512 bytes for NUL bytes (ELF, Mach-O, etc.)
	if ext == "" {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer func() { _ = f.Close() }()
		buf := make([]byte, 512)
		n, err := f.Read(buf)
		if err != nil || n == 0 {
			return false
		}
		for _, b := range buf[:n] {
			if b == 0 {
				return true
			}
		}
	}
	return false
}

func readFileSelection(path string, startLine, endLine int) (string, error) {
	if startLine <= 0 && endLine <= 0 {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine > 0 && endLine < startLine {
		return "", fmt.Errorf("end_line must be >= start_line")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < startLine {
			continue
		}
		if endLine > 0 && lineNo > endLine {
			break
		}
		_, _ = fmt.Fprintf(&b, "%d:%s\n", lineNo, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ─────────────────────────────────────────
// fs.write
// ─────────────────────────────────────────

type fsWriteTool struct{}

func FSWrite() Tool { return &fsWriteTool{} }

func (t *fsWriteTool) Name() string             { return "fs_write" }
func (t *fsWriteTool) Description() string      { return "Write or append content to a file." }
func (t *fsWriteTool) DangerLevel() DangerLevel { return Dangerous }
func (t *fsWriteTool) Effects() ToolEffects     { return ToolEffects{MutatesWorkspace: true} }

func (t *fsWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "content"],
		"properties": {
			"path":    {"type": "string", "description": "File path to write."},
			"content": {"type": "string", "description": "Content to write."},
			"append":  {"type": "boolean", "description": "If true, append instead of overwrite.", "default": false}
		}
	}`)
}

func (t *fsWriteTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"bytes_written": {"type": "integer"}}}`)
}

func (t *fsWriteTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	path, ok := call.Mapper.SecurePath(a.Path)
	if !ok {
		return failResult(start, "illegal path outside sandbox: "+a.Path), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failResult(start, err.Error()), nil
	}

	flag := os.O_CREATE | os.O_WRONLY
	if a.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	defer func() { _ = f.Close() }()

	n, err := f.WriteString(a.Content)
	if err != nil {
		return failResult(start, err.Error()), nil
	}

	digest := sha256.Sum256([]byte(a.Content))
	preview := a.Content
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	previewJSON, _ := json.Marshal(preview)
	return ToolResult{
		OK:         true,
		Output:     fmt.Sprintf(`{"bytes_written":%d,"sha256":"%x","preview":%s}`, n, digest, previewJSON),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// ─────────────────────────────────────────
// fs.list
// ─────────────────────────────────────────

type fsListTool struct{}

func FSList() Tool { return &fsListTool{} }

func (t *fsListTool) Name() string             { return "fs_list" }
func (t *fsListTool) Description() string      { return "List files and directories in a path." }
func (t *fsListTool) DangerLevel() DangerLevel { return Safe }
func (t *fsListTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *fsListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Directory path to list."}
		}
	}`)
}

func (t *fsListTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"entries": {"type": "array", "items": {"type": "string"}}}}`)
}

func (t *fsListTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	path, ok := call.Mapper.SecurePath(a.Path)
	if !ok {
		return failResult(start, "illegal path outside sandbox: "+a.Path), nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		} else if isLowSalienceEntry(n) {
			n += "  [binary/generated — skip]"
		}
		names = append(names, n)
	}
	b, _ := json.Marshal(map[string]any{"entries": names})
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// ─────────────────────────────────────────
// fs.mkdir
// ─────────────────────────────────────────

type fsMkdirTool struct{}

func FSMkdir() Tool { return &fsMkdirTool{} }

func (t *fsMkdirTool) Name() string             { return "fs_mkdir" }
func (t *fsMkdirTool) Description() string      { return "Create a directory (and parents)." }
func (t *fsMkdirTool) DangerLevel() DangerLevel { return Safe }
func (t *fsMkdirTool) Effects() ToolEffects     { return ToolEffects{MutatesWorkspace: true} }

func (t *fsMkdirTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Directory path to create."}
		}
	}`)
}

func (t *fsMkdirTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"created": {"type": "string"}}}`)
}

func (t *fsMkdirTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	path, ok := call.Mapper.SecurePath(a.Path)
	if !ok {
		return failResult(start, "illegal path outside sandbox: "+a.Path), nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return failResult(start, err.Error()), nil
	}
	b, _ := json.Marshal(map[string]string{"created": call.Mapper.ToVirtual(path)})
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func failResult(start time.Time, msg string) ToolResult {
	return ToolResult{
		OK:         false,
		Output:     msg,
		DurationMS: time.Since(start).Milliseconds(),
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/memory"
	"github.com/tripledoublev/v100/internal/providers"
)

type blackboardReadTool struct{}
type blackboardWriteTool struct{}
type blackboardSearchTool struct{}
type blackboardStoreTool struct{}

func BlackboardRead() Tool   { return &blackboardReadTool{} }
func BlackboardWrite() Tool  { return &blackboardWriteTool{} }
func BlackboardSearch() Tool { return &blackboardSearchTool{} }
func BlackboardStore() Tool  { return &blackboardStoreTool{} }

func (t *blackboardReadTool) Name() string { return "blackboard_read" }
func (t *blackboardReadTool) Description() string {
	return "Read shared workspace blackboard content."
}
func (t *blackboardReadTool) DangerLevel() DangerLevel { return Safe }
func (t *blackboardReadTool) Effects() ToolEffects     { return ToolEffects{} }
func (t *blackboardReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *blackboardReadTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)
}
func (t *blackboardReadTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	path := blackboardPath(blackboardWorkspaceDir(call))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult{OK: true, Output: "", DurationMS: time.Since(start).Milliseconds()}, nil
		}
		return failResult(start, "read blackboard: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     string(data),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func (t *blackboardWriteTool) Name() string { return "blackboard_write" }
func (t *blackboardWriteTool) Description() string {
	return "Append or overwrite shared workspace blackboard content."
}
func (t *blackboardWriteTool) DangerLevel() DangerLevel { return Dangerous }
func (t *blackboardWriteTool) Effects() ToolEffects     { return ToolEffects{MutatesRunState: true} }
func (t *blackboardWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"required":["content"],
		"properties":{
			"content":{"type":"string"},
			"append":{"type":"boolean","default":true}
		}
	}`)
}
func (t *blackboardWriteTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"bytes_written":{"type":"integer"}}}`)
}
func (t *blackboardWriteTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Content string `json:"content"`
		Append  *bool  `json:"append"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	appendMode := true
	if a.Append != nil {
		appendMode = *a.Append
	}
	path := blackboardPath(blackboardWorkspaceDir(call))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failResult(start, "mkdir: "+err.Error()), nil
	}

	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
		if a.Content != "" && a.Content[len(a.Content)-1] != '\n' {
			a.Content += "\n"
		}
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return failResult(start, "open: "+err.Error()), nil
	}
	defer func() { _ = f.Close() }()
	n, err := f.WriteString(a.Content)
	if err != nil {
		return failResult(start, "write: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     fmt.Sprintf(`{"bytes_written":%d}`, n),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// ─────────────────────────────────────────
// Vector Tools
// ─────────────────────────────────────────

func (t *blackboardSearchTool) Name() string             { return "blackboard_search" }
func (t *blackboardSearchTool) Description() string      { return "Search vectorized blackboard memory." }
func (t *blackboardSearchTool) DangerLevel() DangerLevel { return Safe }
func (t *blackboardSearchTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}
func (t *blackboardSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["query"],
		"properties": {
			"query": {"type": "string"},
			"limit": {"type": "integer", "default": 5}
		}
	}`)
}
func (t *blackboardSearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"results": {"type": "array"}}}`)
}
func (t *blackboardSearchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if a.Limit <= 0 {
		a.Limit = 5
	}

	if call.Provider == nil {
		return failResult(start, "provider not available for search"), nil
	}

	// 1. Get embedding for query
	embResp, err := call.Provider.Embed(ctx, providers.EmbedRequest{Text: a.Query})
	if err != nil {
		return failResult(start, "embedding query: "+err.Error()), nil
	}

	// 2. Load vector store
	s := memory.NewVectorStore(call.RunID)
	if err := s.Load(); err != nil {
		return failResult(start, "load vector store: "+err.Error()), nil
	}

	// 3. Search
	results := s.Search(embResp.Embedding, a.Limit)

	b, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return failResult(start, "marshal results: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func (t *blackboardStoreTool) Name() string { return "blackboard_store" }
func (t *blackboardStoreTool) Description() string {
	return "Store a memory record in the vectorized blackboard."
}
func (t *blackboardStoreTool) DangerLevel() DangerLevel { return Dangerous }
func (t *blackboardStoreTool) Effects() ToolEffects {
	return ToolEffects{MutatesRunState: true, NeedsNetwork: true, ExternalSideEffect: true}
}
func (t *blackboardStoreTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["content"],
		"properties": {
			"content": {"type": "string"},
			"tags": {"type": "object", "additionalProperties": {"type": "string"}}
		}
	}`)
}
func (t *blackboardStoreTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"ok": {"type": "boolean"}}}`)
}
func (t *blackboardStoreTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Content string            `json:"content"`
		Tags    map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	if call.Provider == nil {
		return failResult(start, "provider not available for storage"), nil
	}

	// 1. Get embedding for content
	embResp, err := call.Provider.Embed(ctx, providers.EmbedRequest{Text: a.Content})
	if err != nil {
		return failResult(start, "embedding content: "+err.Error()), nil
	}

	// 2. Load vector store
	s := memory.NewVectorStore(call.RunID)
	_ = s.Load() // ignore error if not exists

	// 3. Add item
	item := memory.MemoryItem{
		ID:        fmt.Sprintf("mem-%x", time.Now().UnixNano()),
		Content:   a.Content,
		Embedding: embResp.Embedding,
		Metadata: memory.Metadata{
			AgentRole: "", // could be injected from context if available
			StepID:    call.StepID,
			Tags:      a.Tags,
		},
		TS: time.Now().UTC(),
	}
	if err := s.Add(item); err != nil {
		return failResult(start, "add to vector store: "+err.Error()), nil
	}

	return ToolResult{
		OK:         true,
		Output:     `{"ok":true}`,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func blackboardWorkspaceDir(call ToolCallContext) string {
	if root := strings.TrimSpace(call.HostWorkspaceDir); root != "" {
		return root
	}
	if root := strings.TrimSpace(call.WorkspaceDir); root != "" {
		return root
	}
	if runID := strings.TrimSpace(call.RunID); runID != "" {
		return filepath.Join("runs", runID)
	}
	return "runs"
}

func blackboardPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "blackboard.md")
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type categoryDispatchTool struct {
	name        string
	category    string
	description string
	allowed     map[string]bool
}

// NewCategoryDispatch creates a dispatcher that exposes a bounded subset of
// registered tools through one category-level tool surface.
func NewCategoryDispatch(name, category, description string, allowed []string) Tool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, toolName := range allowed {
		toolName = strings.TrimSpace(toolName)
		if toolName != "" && toolName != name {
			allowedSet[toolName] = true
		}
	}
	return &categoryDispatchTool{
		name:        name,
		category:    category,
		description: description,
		allowed:     allowedSet,
	}
}

func (t *categoryDispatchTool) Name() string { return t.name }

func (t *categoryDispatchTool) Description() string {
	return t.description
}

func (t *categoryDispatchTool) DangerLevel() DangerLevel { return Dangerous }

func (t *categoryDispatchTool) Effects() ToolEffects {
	return ToolEffects{
		MutatesWorkspace:   true,
		MutatesRunState:    true,
		NeedsNetwork:       true,
		ExternalSideEffect: true,
	}
}

func (t *categoryDispatchTool) InputSchema() json.RawMessage {
	names := make([]string, 0, len(t.allowed))
	for name := range t.allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	b, _ := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"tool", "args"},
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"description": fmt.Sprintf("Tool from the %s category to run.", t.category),
				"enum":        names,
			},
			"args": map[string]any{
				"type":        "object",
				"description": "Arguments to pass to the selected tool.",
			},
		},
	})
	return b
}

func (t *categoryDispatchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"},"output":{"type":"string"},"tool":{"type":"string"},"category":{"type":"string"}}}`)
}

func (t *categoryDispatchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var req struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error(), DurationMS: time.Since(start).Milliseconds()}, nil
	}
	req.Tool = strings.TrimSpace(req.Tool)
	if req.Tool == "" {
		return ToolResult{OK: false, Output: "tool is required", DurationMS: time.Since(start).Milliseconds()}, nil
	}
	if !t.allowed[req.Tool] {
		return ToolResult{OK: false, Output: fmt.Sprintf("tool %q is not in category %q", req.Tool, t.category), DurationMS: time.Since(start).Milliseconds()}, nil
	}
	if call.Registry == nil {
		return ToolResult{OK: false, Output: "category dispatcher not wired: missing registry", DurationMS: time.Since(start).Milliseconds()}, nil
	}
	target, ok := call.Registry.Lookup(req.Tool)
	if !ok {
		return ToolResult{OK: false, Output: fmt.Sprintf("tool %q is not registered", req.Tool), DurationMS: time.Since(start).Milliseconds()}, nil
	}
	childCall := call
	childCall.CallID = call.CallID + "/" + req.Tool
	result, err := target.Exec(ctx, childCall, req.Args)
	result.DurationMS = time.Since(start).Milliseconds()
	return result, err
}

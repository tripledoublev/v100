//go:build !windows
// +build !windows

package tools

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type fsOutlineTool struct{}

func FSOutline() Tool { return &fsOutlineTool{} }

func (t *fsOutlineTool) Name() string { return "fs_outline" }
func (t *fsOutlineTool) Description() string {
	return "List functions and types in a file using semantic parsing."
}
func (t *fsOutlineTool) DangerLevel() DangerLevel { return Safe }
func (t *fsOutlineTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *fsOutlineTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "File path to outline."}
		}
	}`)
}

func (t *fsOutlineTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"entities": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"type": {"type": "string"},
						"line_start": {"type": "integer"},
						"line_end": {"type": "integer"}
					}
				}
			}
		}
	}`)
}

func (t *fsOutlineTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
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
	content, err := os.ReadFile(path)
	if err != nil {
		return failResult(start, "read file: "+err.Error()), nil
	}

	ext := filepath.Ext(path)
	if ext != ".go" {
		return failResult(start, "fs_outline currently only supports .go files"), nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return failResult(start, "parse error: "+err.Error()), nil
	}

	var entities []map[string]any
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			kind := "function"
			if node.Recv != nil {
				kind = "method"
			}
			entities = append(entities, map[string]any{
				"name":       node.Name.Name,
				"type":       kind,
				"line_start": fset.Position(node.Pos()).Line,
				"line_end":   fset.Position(node.End()).Line,
			})
		case *ast.GenDecl:
			if node.Tok != token.TYPE {
				continue
			}
			for _, spec := range node.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				entities = append(entities, map[string]any{
					"name":       typeSpec.Name.Name,
					"type":       "type",
					"line_start": fset.Position(typeSpec.Pos()).Line,
					"line_end":   fset.Position(typeSpec.End()).Line,
				})
			}
		}
	}

	sort.SliceStable(entities, func(i, j int) bool {
		li, _ := entities[i]["line_start"].(int)
		lj, _ := entities[j]["line_start"].(int)
		if li != lj {
			return li < lj
		}
		return entities[i]["name"].(string) < entities[j]["name"].(string)
	})

	b, _ := json.Marshal(map[string]any{"entities": entities})
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

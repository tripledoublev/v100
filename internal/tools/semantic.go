package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
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

	lang := golang.GetLanguage()
	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return failResult(start, "parse error: "+err.Error()), nil
	}

	var entities []map[string]any

	// Query for function declarations and type declarations in Go
	queryString := `
		(function_declaration name: (identifier) @func_name)
		(method_declaration name: (field_identifier) @method_name)
		(type_declaration (type_spec name: (type_identifier) @type_name))
	`
	q, err := sitter.NewQuery([]byte(queryString), lang)
	if err != nil {
		return failResult(start, "query init error: "+err.Error()), nil
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		for _, capture := range m.Captures {
			node := capture.Node
			name := node.Content(content)

			// Get the parent declaration node to find the full range
			declNode := node.Parent()
			if capture.Index == 0 || capture.Index == 1 { // function or method
				for declNode != nil && declNode.Type() != "function_declaration" && declNode.Type() != "method_declaration" {
					declNode = declNode.Parent()
				}
			} else { // type
				for declNode != nil && declNode.Type() != "type_declaration" {
					declNode = declNode.Parent()
				}
			}

			if declNode == nil {
				declNode = node
			}

			startPoint := declNode.StartPoint()
			endPoint := declNode.EndPoint()

			typeName := "function"
			if capture.Index == 1 {
				typeName = "method"
			} else if capture.Index == 2 {
				typeName = "type"
			}

			entities = append(entities, map[string]any{
				"name":       name,
				"type":       typeName,
				"line_start": startPoint.Row + 1,
				"line_end":   endPoint.Row + 1,
			})
		}
	}

	b, _ := json.Marshal(map[string]any{"entities": entities})
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFSOutline(t *testing.T) {
	dir := t.TempDir()
	code := `package main

import "fmt"

// Hello function
func Hello(name string) string {
	return fmt.Sprintf("Hello, %s", name)
}

type Greeter struct{}

func (g *Greeter) Greet() {
	fmt.Println("Hi")
}
`
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := FSOutline()
	ctx := context.Background()
	call := ToolCallContext{
		WorkspaceDir: dir,
	}
	args := json.RawMessage(`{"path": "test.go"}`)

	res, err := tool.Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}

	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}

	var out struct {
		Entities []map[string]any `json:"entities"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}

	foundHello := false
	foundGreet := false
	foundGreeter := false

	for _, e := range out.Entities {
		name := e["name"].(string)
		typ := e["type"].(string)
		if name == "Hello" && typ == "function" {
			foundHello = true
		}
		if name == "Greet" && typ == "method" {
			foundGreet = true
		}
		if name == "Greeter" && typ == "type" {
			foundGreeter = true
		}
	}

	if !foundHello {
		t.Error("Hello function not found in outline")
	}
	if !foundGreet {
		t.Error("Greet method not found in outline")
	}
	if !foundGreeter {
		t.Error("Greeter type not found in outline")
	}
}

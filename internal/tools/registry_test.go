package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/tools"
)

func TestRegistryEnabled(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs.read", "fs.list"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.FSList())

	// fs.read is enabled
	if _, ok := reg.Get("fs.read"); !ok {
		t.Error("fs.read should be enabled")
	}

	// fs.write is registered but NOT in enabled list
	if _, ok := reg.Get("fs.write"); ok {
		t.Error("fs.write should not be accessible (not enabled)")
	}

	// fs.list is enabled
	if _, ok := reg.Get("fs.list"); !ok {
		t.Error("fs.list should be enabled")
	}
}

func TestRegistryDangerLevel(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs.read", "fs.write", "sh"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.Sh())

	if reg.IsDangerous("fs.read") {
		t.Error("fs.read should not be dangerous")
	}
	if !reg.IsDangerous("fs.write") {
		t.Error("fs.write should be dangerous")
	}
	if !reg.IsDangerous("sh") {
		t.Error("sh should be dangerous")
	}
}

func TestRegistrySpecs(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs.read", "fs.list"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSList())

	specs := reg.Specs()
	if len(specs) != 2 {
		t.Errorf("expected 2 specs, got %d", len(specs))
	}
	for _, s := range specs {
		if s.Name == "" {
			t.Error("spec name should not be empty")
		}
		if s.InputSchema == nil {
			t.Errorf("spec %s should have input schema", s.Name)
		}
	}
}

func TestRegistryValidate(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs.read", "nonexistent"})
	reg.Register(tools.FSRead())

	if err := reg.Validate(); err == nil {
		t.Error("expected validation error for nonexistent tool")
	}
}

func TestFSReadExec(t *testing.T) {
	// Write a temp file
	dir := t.TempDir()
	content := "hello world\n"

	// Write file first using FSWrite
	writeTool := tools.FSWrite()
	ctx := context.Background()
	call := tools.ToolCallContext{WorkspaceDir: dir}

	writeArgs, _ := json.Marshal(map[string]string{
		"path":    "test.txt",
		"content": content,
	})
	res, err := writeTool.Exec(ctx, call, writeArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("write failed: %s", res.Output)
	}

	// Now read it back
	readTool := tools.FSRead()
	readArgs, _ := json.Marshal(map[string]string{"path": "test.txt"})
	res, err = readTool.Exec(ctx, call, readArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("read failed: %s", res.Output)
	}
	if res.Output != content {
		t.Errorf("expected %q, got %q", content, res.Output)
	}
}

func TestFSListExec(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	call := tools.ToolCallContext{WorkspaceDir: dir}

	// Create some files via FSMkdir and FSWrite
	mkdirTool := tools.FSMkdir()
	mkArgs, _ := json.Marshal(map[string]string{"path": "subdir"})
	if _, err := mkdirTool.Exec(ctx, call, mkArgs); err != nil {
		t.Fatal(err)
	}

	listTool := tools.FSList()
	listArgs, _ := json.Marshal(map[string]string{"path": "."})
	res, err := listTool.Exec(ctx, call, listArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("list failed: %s", res.Output)
	}

	var out struct {
		Entries []string `json:"entries"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range out.Entries {
		if e == "subdir/" {
			found = true
		}
	}
	if !found {
		t.Errorf("subdir/ not found in listing: %v", out.Entries)
	}
}

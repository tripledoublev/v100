package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true}
}
func (m *mockProvider) Complete(ctx context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{}, nil
}
func (m *mockProvider) Embed(ctx context.Context, req providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (m *mockProvider) Metadata(ctx context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: "mock", ContextSize: 4096}, nil
}

func TestRegistryEnabled(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "fs_list"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.FSList())

	// fs_read is enabled
	if _, ok := reg.Get("fs_read"); !ok {
		t.Error("fs_read should be enabled")
	}

	// fs_write is registered but NOT in enabled list
	if _, ok := reg.Get("fs_write"); ok {
		t.Error("fs_write should not be accessible (not enabled)")
	}

	// fs_list is enabled
	if _, ok := reg.Get("fs_list"); !ok {
		t.Error("fs_list should be enabled")
	}
}

func TestRegistryDangerLevel(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "fs_write", "sh"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.Sh())

	if reg.IsDangerous("fs_read") {
		t.Error("fs_read should not be dangerous")
	}
	if !reg.IsDangerous("fs_write") {
		t.Error("fs_write should be dangerous")
	}
	if !reg.IsDangerous("sh") {
		t.Error("sh should be dangerous")
	}
}

func TestRegistryEffects(t *testing.T) {
	reg := tools.NewRegistry([]string{
		"fs_mkdir",
		"blackboard_write",
		"curl_fetch",
		"git_push",
		"sh",
	})
	reg.Register(tools.FSMkdir())
	reg.Register(tools.BlackboardWrite())
	reg.Register(tools.CurlFetch())
	reg.Register(tools.GitPush())
	reg.Register(tools.Sh())

	if eff := reg.Effects("fs_mkdir"); !eff.MutatesWorkspace || eff.MutatesRunState {
		t.Fatalf("fs_mkdir effects = %+v, want workspace mutation only", eff)
	}
	if eff := reg.Effects("blackboard_write"); !eff.MutatesRunState || eff.MutatesWorkspace {
		t.Fatalf("blackboard_write effects = %+v, want run-state mutation only", eff)
	}
	if eff := reg.Effects("curl_fetch"); !eff.NeedsNetwork || !eff.ExternalSideEffect {
		t.Fatalf("curl_fetch effects = %+v, want network + external side effect", eff)
	}
	if eff := reg.Effects("git_push"); !eff.MutatesWorkspace || !eff.NeedsNetwork || !eff.ExternalSideEffect {
		t.Fatalf("git_push effects = %+v, want workspace mutation + network + external side effect", eff)
	}
	if eff := reg.Effects("sh"); !eff.MutatesWorkspace || !eff.NeedsNetwork || !eff.ExternalSideEffect {
		t.Fatalf("sh effects = %+v, want workspace mutation + network + external side effect", eff)
	}
	if eff := reg.Effects("nonexistent"); eff != (tools.ToolEffects{}) {
		t.Fatalf("unknown tool effects = %+v, want zero value", eff)
	}
}

func TestRegistrySpecs(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "fs_list"})
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
	reg := tools.NewRegistry([]string{"fs_read", "nonexistent"})
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
	call := tools.ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &tools.MockMapper{Dir: dir},
	}

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
	call := tools.ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &tools.MockMapper{Dir: dir},
	}

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

package tools_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/tools"
)

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

func TestRegistryDynamicRegistration(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read"})
	reg.Register(tools.FSRead())

	if reg.IsEnabled("fs_write") {
		t.Fatal("fs_write should start disabled")
	}
	if _, ok := reg.Lookup("fs_write"); ok {
		t.Fatal("fs_write should not be registered yet")
	}

	reg.RegisterAndEnable(tools.FSWrite())

	if !reg.IsEnabled("fs_write") {
		t.Fatal("fs_write should be enabled after RegisterAndEnable")
	}
	if _, ok := reg.Get("fs_write"); !ok {
		t.Fatal("fs_write should be accessible after dynamic registration")
	}
	if err := reg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRegistryEnableDisableUnregister(t *testing.T) {
	reg := tools.NewRegistry(nil)
	reg.Register(tools.FSRead())

	if _, ok := reg.Get("fs_read"); ok {
		t.Fatal("fs_read should not be accessible before enable")
	}

	reg.Enable("fs_read")
	if _, ok := reg.Get("fs_read"); !ok {
		t.Fatal("fs_read should be accessible after enable")
	}

	reg.Disable("fs_read")
	if _, ok := reg.Get("fs_read"); ok {
		t.Fatal("fs_read should not be accessible after disable")
	}

	reg.Unregister("fs_read")
	if _, ok := reg.Lookup("fs_read"); ok {
		t.Fatal("fs_read should not be registered after unregister")
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
		"web_extract",
		"news_fetch",
		"git_push",
		"sh",
	})
	reg.Register(tools.FSMkdir())
	reg.Register(tools.BlackboardWrite())
	reg.Register(tools.CurlFetch())
	reg.Register(tools.WebExtract())
	reg.Register(tools.NewsFetch())
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
	if eff := reg.Effects("web_extract"); !eff.NeedsNetwork || !eff.ExternalSideEffect {
		t.Fatalf("web_extract effects = %+v, want network + external side effect", eff)
	}
	if eff := reg.Effects("news_fetch"); !eff.NeedsNetwork || !eff.ExternalSideEffect {
		t.Fatalf("news_fetch effects = %+v, want network + external side effect", eff)
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

func TestRegistryRegisteredTools(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read"})
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())

	registered := reg.RegisteredTools()
	if len(registered) != 2 {
		t.Fatalf("expected 2 registered tools, got %d", len(registered))
	}
	if registered[0].Name() != "fs_read" || registered[1].Name() != "fs_write" {
		t.Fatalf("registered tools order = [%s %s], want [fs_read fs_write]", registered[0].Name(), registered[1].Name())
	}
}

func TestRegistryValidate(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "nonexistent"})
	reg.Register(tools.FSRead())

	if err := reg.Validate(); err == nil {
		t.Error("expected validation error for nonexistent tool")
	}
}

type badTool struct {
	name string
	desc string
	in   string
}

func (b *badTool) Name() string                   { return b.name }
func (b *badTool) Description() string            { return b.desc }
func (b *badTool) InputSchema() json.RawMessage   { return json.RawMessage(b.in) }
func (b *badTool) OutputSchema() json.RawMessage  { return nil }
func (b *badTool) DangerLevel() tools.DangerLevel { return tools.Safe }
func (b *badTool) Effects() tools.ToolEffects     { return tools.ToolEffects{} }
func (b *badTool) Exec(context.Context, tools.ToolCallContext, json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{}, nil
}

func TestRegistryValidateToolSurface(t *testing.T) {
	t.Run("empty description", func(t *testing.T) {
		reg := tools.NewRegistry([]string{"bad"})
		reg.Register(&badTool{name: "bad", desc: "", in: `{"type":"object"}`})
		if err := reg.Validate(); err == nil || !strings.Contains(err.Error(), "empty description") {
			t.Errorf("expected empty description error, got %v", err)
		}
	})

	t.Run("empty schema", func(t *testing.T) {
		reg := tools.NewRegistry([]string{"bad"})
		reg.Register(&badTool{name: "bad", desc: "desc", in: ""})
		if err := reg.Validate(); err == nil || !strings.Contains(err.Error(), "empty or null input schema") {
			t.Errorf("expected empty schema error, got %v", err)
		}
	})

	t.Run("null schema", func(t *testing.T) {
		reg := tools.NewRegistry([]string{"bad"})
		reg.Register(&badTool{name: "bad", desc: "desc", in: "null"})
		if err := reg.Validate(); err == nil || !strings.Contains(err.Error(), "empty or null input schema") {
			t.Errorf("expected null schema error, got %v", err)
		}
	})

	t.Run("valid empty object schema", func(t *testing.T) {
		reg := tools.NewRegistry([]string{"bad"})
		reg.Register(&badTool{name: "bad", desc: "desc", in: "{}"})
		if err := reg.Validate(); err != nil {
			t.Errorf("expected no error for {}, got %v", err)
		}
	})
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

func TestFSReadExecLineRange(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	call := tools.ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &tools.MockMapper{Dir: dir},
	}

	writeTool := tools.FSWrite()
	writeArgs, _ := json.Marshal(map[string]string{
		"path":    "test.txt",
		"content": "alpha\nbeta\ngamma\ndelta\n",
	})
	if _, err := writeTool.Exec(ctx, call, writeArgs); err != nil {
		t.Fatal(err)
	}

	readTool := tools.FSRead()
	readArgs, _ := json.Marshal(map[string]any{
		"path":       "test.txt",
		"start_line": 2,
		"end_line":   3,
	})
	res, err := readTool.Exec(ctx, call, readArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("read failed: %s", res.Output)
	}
	want := "2:beta\n3:gamma\n"
	if res.Output != want {
		t.Fatalf("expected %q, got %q", want, res.Output)
	}
}

func TestProjectSearchExecWithContextLines(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed")
	}

	dir := t.TempDir()
	ctx := context.Background()
	call := tools.ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &tools.MockMapper{Dir: dir},
	}

	writeTool := tools.FSWrite()
	writeArgs, _ := json.Marshal(map[string]string{
		"path": "sample.txt",
		"content": "zero\nalpha target\nbeta\ngamma\n" +
			"delta target\nepsilon\n",
	})
	if _, err := writeTool.Exec(ctx, call, writeArgs); err != nil {
		t.Fatal(err)
	}

	searchTool := tools.ProjectSearch()
	searchArgs, _ := json.Marshal(map[string]any{
		"pattern":       "target",
		"path":          ".",
		"context_lines": 1,
		"max_results":   20,
	})
	res, err := searchTool.Exec(ctx, call, searchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("search failed: %s", res.Output)
	}
	for _, want := range []string{
		"sample.txt-1-zero",
		"sample.txt:2:alpha target",
		"sample.txt-3-beta",
		"sample.txt-4-gamma",
		"sample.txt:5:delta target",
		"sample.txt-6-epsilon",
	} {
		if !containsLine(res.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, res.Output)
		}
	}
}

func containsLine(out, want string) bool {
	for _, line := range strings.Split(out, "\n") {
		if line == want || strings.HasSuffix(line, want) {
			return true
		}
	}
	return false
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

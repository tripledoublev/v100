package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/tools"
)

func TestInspectTool(t *testing.T) {
	reg := tools.NewRegistry([]string{"fs_read", "inspect_tool", "translate"})
	reg.Register(tools.FSRead())
	reg.Register(tools.InspectTool())
	reg.Register(tools.Translate())

	ctx := context.Background()
	call := tools.ToolCallContext{
		Registry: reg,
	}

	// Test inspecting existing tool
	args := json.RawMessage(`{"tool_name": "fs_read"}`)
	res, err := tools.InspectTool().Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}

	var out struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "fs_read" {
		t.Errorf("expected name fs_read, got %s", out.Name)
	}
	if out.Description == "" {
		t.Error("expected non-empty description")
	}

	args = json.RawMessage(`{"tool_name": "translate"}`)
	res, err = tools.InspectTool().Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("translate inspect failed: %s", res.Output)
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "translate" {
		t.Errorf("expected name translate, got %s", out.Name)
	}

	// Test inspecting non-existent tool
	args = json.RawMessage(`{"tool_name": "non_existent"}`)
	res, err = tools.InspectTool().Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("expected tool to fail for non-existent tool")
	}
}

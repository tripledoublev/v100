package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type categoryStubTool struct{}

func (categoryStubTool) Name() string                  { return "stub_tool" }
func (categoryStubTool) Description() string           { return "stub tool" }
func (categoryStubTool) InputSchema() json.RawMessage  { return json.RawMessage(`{"type":"object"}`) }
func (categoryStubTool) OutputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (categoryStubTool) DangerLevel() DangerLevel      { return Safe }
func (categoryStubTool) Effects() ToolEffects          { return ToolEffects{} }
func (categoryStubTool) Exec(context.Context, ToolCallContext, json.RawMessage) (ToolResult, error) {
	return ToolResult{OK: true, Output: "stub ran"}, nil
}

func TestCategoryDispatchExecutesAllowedRegisteredTool(t *testing.T) {
	reg := NewRegistry([]string{"tools_test"})
	reg.Register(categoryStubTool{})
	dispatcher := NewCategoryDispatch("tools_test", "test", "test dispatcher", []string{"stub_tool"})
	reg.Register(dispatcher)

	args := json.RawMessage(`{"tool":"stub_tool","args":{}}`)
	res, err := dispatcher.Exec(context.Background(), ToolCallContext{CallID: "c1", Registry: reg}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Output != "stub ran" {
		t.Fatalf("dispatch result = %+v", res)
	}
}

func TestCategoryDispatchRejectsOutsideCategory(t *testing.T) {
	reg := NewRegistry([]string{"tools_test"})
	reg.Register(categoryStubTool{})
	dispatcher := NewCategoryDispatch("tools_test", "test", "test dispatcher", []string{"other_tool"})
	reg.Register(dispatcher)

	res, err := dispatcher.Exec(context.Background(), ToolCallContext{CallID: "c1", Registry: reg}, json.RawMessage(`{"tool":"stub_tool","args":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Output, "not in category") {
		t.Fatalf("dispatch should reject outside-category tool, got %+v", res)
	}
}

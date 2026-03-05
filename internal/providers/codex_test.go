package providers

import (
	"encoding/json"
	"testing"
)

func TestCodexConvertMessagesToolOutputAlwaysPresent(t *testing.T) {
	_, input := codexConvertMessages([]Message{
		{
			Role:       "tool",
			ToolCallID: "call-1",
			Name:       "git_push",
			Content:    "",
		},
	})
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	if input[0].Type != "function_call_output" {
		t.Fatalf("expected function_call_output, got %q", input[0].Type)
	}
	if input[0].CallID != "call-1" {
		t.Fatalf("expected call_id call-1, got %q", input[0].CallID)
	}
	if input[0].Output == nil || *input[0].Output == "" {
		t.Fatal("expected non-empty output field")
	}
}

func TestCodexConvertMessagesUserInputHasNoOutputField(t *testing.T) {
	_, input := codexConvertMessages([]Message{
		{Role: "user", Content: "hello"},
	})
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	b, err := json.Marshal(input[0])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := obj["output"]; ok {
		t.Fatalf("unexpected output field in user input payload: %s", string(b))
	}
}

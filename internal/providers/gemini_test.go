package providers

import (
	"encoding/json"
	"testing"
)

func TestGeminiConvertMessagesMultipleToolResponsesMerged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Read two files"},
		{Role: "assistant", Content: "I'll inspect both.", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "fs_read", Args: json.RawMessage(`{"path":"a.txt"}`)},
			{ID: "tc2", Name: "fs_read", Args: json.RawMessage(`{"path":"b.txt"}`)},
		}},
		{Role: "tool", Content: "contents of a", ToolCallID: "tc1", Name: "fs_read"},
		{Role: "tool", Content: "contents of b", ToolCallID: "tc2", Name: "fs_read"},
		{Role: "assistant", Content: "Done."},
	}

	sys, converted := geminiConvertMessages(msgs)
	if sys != nil {
		t.Fatalf("expected no system instruction, got %+v", sys)
	}
	if len(converted) != 4 {
		t.Fatalf("expected 4 content turns, got %d", len(converted))
	}

	if converted[1].Role != "model" {
		t.Fatalf("assistant turn role = %q, want model", converted[1].Role)
	}
	if got := len(converted[1].Parts); got != 3 {
		t.Fatalf("assistant turn parts = %d, want 3", got)
	}

	if converted[2].Role != "user" {
		t.Fatalf("tool response turn role = %q, want user", converted[2].Role)
	}
	if got := len(converted[2].Parts); got != 2 {
		t.Fatalf("tool response parts = %d, want 2", got)
	}
	for i, part := range converted[2].Parts {
		if part.FunctionResponse == nil {
			t.Fatalf("tool response part %d missing functionResponse", i)
		}
		if part.FunctionResponse.Name != "fs_read" {
			t.Fatalf("tool response name = %q, want fs_read", part.FunctionResponse.Name)
		}
	}
}

func TestGeminiConvertMessagesEmptyToolOutputNormalized(t *testing.T) {
	_, converted := geminiConvertMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "git_push", Args: json.RawMessage(`{}`)},
		}},
		{Role: "tool", ToolCallID: "tc1", Name: "git_push", Content: ""},
	})

	if len(converted) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(converted))
	}
	resp := converted[1].Parts[0].FunctionResponse
	if resp == nil {
		t.Fatal("expected functionResponse part")
	}
	got, _ := resp.Response["result"].(string)
	if got != "(no output)" {
		t.Fatalf("tool result = %q, want (no output)", got)
	}
}

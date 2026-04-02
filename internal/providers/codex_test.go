package providers

import (
	"encoding/json"
	"strings"
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

func TestCodexConvertMessagesUserImagesBecomeInputImageItems(t *testing.T) {
	_, input := codexConvertMessages([]Message{
		{
			Role:    "user",
			Content: "what is in this image?",
			Images: []ImageAttachment{{
				MIMEType: "image/png",
				Data:     []byte("png-bytes"),
			}},
		},
	})
	if len(input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(input))
	}
	if input[0].Type != "message" {
		t.Fatalf("expected message input type, got %q", input[0].Type)
	}
	parts, ok := input[0].Content.([]codexInputContent)
	if !ok {
		t.Fatalf("expected structured content, got %#v", input[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("expected text and image parts, got %d", len(parts))
	}
	if parts[0].Type != "input_text" || parts[0].Text != "what is in this image?" {
		t.Fatalf("unexpected text part: %#v", parts[0])
	}
	if parts[1].Type != "input_image" {
		t.Fatalf("unexpected image part type: %#v", parts[1])
	}
	if parts[1].Detail != "auto" {
		t.Fatalf("expected auto detail, got %q", parts[1].Detail)
	}
	if !strings.HasPrefix(parts[1].ImageURL, "data:image/png;base64,") {
		t.Fatalf("unexpected image data URL: %q", parts[1].ImageURL)
	}
}

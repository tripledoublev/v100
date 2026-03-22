package providers

import (
	"encoding/json"
	"os"
	"strings"
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

func TestGeminiConvertMessagesWithImage(t *testing.T) {
	_, converted := geminiConvertMessages([]Message{
		{
			Role:    "user",
			Content: "What is in this image?",
			Images: []ImageAttachment{{
				MIMEType: "image/png",
				Data:     []byte{0x89, 0x50, 0x4e, 0x47},
			}},
		},
	})

	if len(converted) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(converted))
	}
	if converted[0].Role != "user" {
		t.Fatalf("role = %q, want user", converted[0].Role)
	}
	if len(converted[0].Parts) != 2 {
		t.Fatalf("expected text and image parts, got %d", len(converted[0].Parts))
	}
	if converted[0].Parts[0].Text != "What is in this image?" {
		t.Fatalf("unexpected text part: %#v", converted[0].Parts[0])
	}
	if converted[0].Parts[1].InlineData == nil {
		t.Fatalf("expected inlineData part, got %#v", converted[0].Parts[1])
	}
	if converted[0].Parts[1].InlineData.MIMEType != "image/png" {
		t.Fatalf("mimeType = %q, want image/png", converted[0].Parts[1].InlineData.MIMEType)
	}
	if !strings.HasPrefix(converted[0].Parts[1].InlineData.Data, "iVBORw") {
		t.Fatalf("expected base64 PNG payload, got %q", converted[0].Parts[1].InlineData.Data)
	}
}

func TestResolveGeminiEmbeddingAPIKeyPrefersGeminiSpecificVar(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "google-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	if got := resolveGeminiEmbeddingAPIKey(); got != "gemini-key" {
		t.Fatalf("resolveGeminiEmbeddingAPIKey() = %q, want gemini-key", got)
	}
}

func TestResolveGeminiEmbeddingAPIKeyFallsBackToGoogleAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "google-key")

	if got := resolveGeminiEmbeddingAPIKey(); got != "google-key" {
		t.Fatalf("resolveGeminiEmbeddingAPIKey() = %q, want google-key", got)
	}
}

func TestGeminiEmbedRequiresAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	p := &GeminiProvider{}
	_, err := p.Embed(t.Context(), EmbedRequest{Text: "hello"})
	if err == nil {
		t.Fatal("expected embed to require API key")
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("error = %q, want API key guidance", err)
	}
}

func TestResolveGeminiEmbeddingAPIKeyIgnoresWhitespace(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "   ")
	t.Setenv("GOOGLE_API_KEY", "\tgoogle-key\n")

	if got := resolveGeminiEmbeddingAPIKey(); got != "google-key" {
		t.Fatalf("resolveGeminiEmbeddingAPIKey() = %q, want google-key", got)
	}
}

func TestResolveGeminiEmbeddingAPIKeyEmpty(t *testing.T) {
	_ = os.Unsetenv("GEMINI_API_KEY")
	_ = os.Unsetenv("GOOGLE_API_KEY")

	if got := resolveGeminiEmbeddingAPIKey(); got != "" {
		t.Fatalf("resolveGeminiEmbeddingAPIKey() = %q, want empty", got)
	}
}

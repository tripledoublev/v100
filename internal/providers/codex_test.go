package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/auth"
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

func TestCodexCompleteRejectsMaxTokensBeforeHTTP(t *testing.T) {
	var called bool
	prov := newTestCodexProvider(func(*http.Request) (*http.Response, error) {
		called = true
		return codexTestStreamResponse(), nil
	})

	_, err := prov.Complete(context.Background(), CompleteRequest{
		Messages:  []Message{{Role: "user", Content: "hello"}},
		GenParams: GenParams{MaxTokens: 700},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported generation parameter max_tokens") {
		t.Fatalf("expected local max_tokens error, got %v", err)
	}
	if called {
		t.Fatal("HTTP transport should not be called for unsupported max_tokens")
	}
}

func TestCodexStreamCompleteRejectsMaxTokensBeforeHTTP(t *testing.T) {
	var called bool
	prov := newTestCodexProvider(func(*http.Request) (*http.Response, error) {
		called = true
		return codexTestStreamResponse(), nil
	})

	ch, err := prov.StreamComplete(context.Background(), CompleteRequest{
		Messages:  []Message{{Role: "user", Content: "hello"}},
		GenParams: GenParams{MaxTokens: 700},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported generation parameter max_tokens") {
		t.Fatalf("expected local max_tokens error, got %v", err)
	}
	if ch != nil {
		t.Fatal("expected nil stream channel on unsupported max_tokens")
	}
	if called {
		t.Fatal("HTTP transport should not be called for unsupported max_tokens")
	}
}

func TestCodexCompleteRequestOmitsMaxOutputTokens(t *testing.T) {
	var body []byte
	prov := newTestCodexProvider(func(req *http.Request) (*http.Response, error) {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		return codexTestStreamResponse(), nil
	})

	if _, err := prov.Complete(context.Background(), CompleteRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if strings.Contains(string(body), "max_output_tokens") {
		t.Fatalf("Codex request should not include max_output_tokens: %s", body)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestCodexProvider(rt roundTripFunc) *CodexProvider {
	return &CodexProvider{
		token: auth.Token{
			Access:    "access-token",
			AccountID: "account-id",
			ExpiresMS: time.Now().Add(time.Hour).UnixMilli(),
		},
		defaultModel: codexDefaultModel,
		client:       &http.Client{Transport: rt},
	}
}

func codexTestStreamResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			"event: response.output_text.delta\n" +
				"data: {\"delta\":\"ok\"}\n\n" +
				"event: response.completed\n" +
				"data: {\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
				"data: [DONE]\n\n",
		)),
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

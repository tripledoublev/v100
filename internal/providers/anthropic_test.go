package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAnthropicConvertMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "Read file"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "fs_read", Args: json.RawMessage(`{"path":"test.txt"}`)},
		}},
		{Role: "tool", Content: "file contents", ToolCallID: "tc1", Name: "fs_read"},
		{Role: "assistant", Content: "Got it."},
	}

	system, converted := anthropicConvertMessages(msgs)

	if system != "You are helpful." {
		t.Errorf("expected system prompt, got %q", system)
	}

	// Expected: user, assistant, user, assistant(tool_use), user(tool_result), assistant
	if len(converted) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(converted))
	}

	if converted[0].Role != "user" {
		t.Errorf("msg[0]: expected user, got %s", converted[0].Role)
	}
	if converted[1].Role != "assistant" {
		t.Errorf("msg[1]: expected assistant, got %s", converted[1].Role)
	}
	if converted[3].Role != "assistant" {
		t.Errorf("msg[3]: expected assistant, got %s", converted[3].Role)
	}
	// Tool result should be a user message with content blocks
	if converted[4].Role != "user" {
		t.Errorf("msg[4]: expected user (tool_result), got %s", converted[4].Role)
	}
}

func TestAnthropicConvertMessagesMultipleTools(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Do two things"},
		{Role: "assistant", Content: "I'll do both", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "fs_read", Args: json.RawMessage(`{"path":"a.txt"}`)},
			{ID: "tc2", Name: "fs_read", Args: json.RawMessage(`{"path":"b.txt"}`)},
		}},
		{Role: "tool", Content: "contents of a", ToolCallID: "tc1", Name: "fs_read"},
		{Role: "tool", Content: "contents of b", ToolCallID: "tc2", Name: "fs_read"},
	}

	_, converted := anthropicConvertMessages(msgs)

	// user, assistant(text+2 tool_use), user(2 tool_results)
	if len(converted) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(converted))
	}

	// The tool results should be merged into a single user message
	if converted[2].Role != "user" {
		t.Errorf("msg[2]: expected user (merged tool_results), got %s", converted[2].Role)
	}
	blocks, ok := converted[2].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("msg[2]: expected content blocks, got %T", converted[2].Content)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 tool_result blocks, got %d", len(blocks))
	}
}

func TestAnthropicEstimateCost(t *testing.T) {
	tests := []struct {
		model  string
		input  int
		output int
		min    float64
		max    float64
	}{
		{"claude-sonnet-4-20250514", 1000, 100, 0.001, 0.01},
		{"claude-opus-4-20250514", 1000, 100, 0.01, 0.1},
		{"claude-haiku-3-5-20241022", 1000, 100, 0.0001, 0.01},
	}

	for _, tt := range tests {
		cost := anthropicEstimateCost(tt.model, tt.input, tt.output)
		if cost < tt.min || cost > tt.max {
			t.Errorf("cost for %s: %.6f not in [%.6f, %.6f]", tt.model, cost, tt.min, tt.max)
		}
	}
}

func TestAnthropicParseResponse(t *testing.T) {
	raw := `{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Hello!"},
			{"type": "tool_use", "id": "tc1", "name": "fs_read", "input": {"path": "test.txt"}}
		],
		"usage": {"input_tokens": 100, "output_tokens": 50},
		"stop_reason": "end_turn"
	}`

	resp, err := anthropicParseResponse("claude-sonnet-4-20250514", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if resp.AssistantText != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", resp.AssistantText)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "fs_read" {
		t.Errorf("expected fs_read, got %s", resp.ToolCalls[0].Name)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestResolveAnthropicKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Write stored key
	authDir := filepath.Join(dir, "v100")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "anthropic_auth.json"), []byte(`{"api_key":"sk-ant-stored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also set env var — stored key should take priority
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-envvar")

	key := resolveAnthropicKey("ANTHROPIC_API_KEY")
	if key != "sk-ant-stored" {
		t.Errorf("expected stored key, got %q", key)
	}
}

func TestResolveAnthropicKeyFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // no auth file exists
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-envvar")

	key := resolveAnthropicKey("ANTHROPIC_API_KEY")
	if key != "sk-ant-envvar" {
		t.Errorf("expected env key, got %q", key)
	}
}

func TestResolveAnthropicKeyEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ANTHROPIC_API_KEY", "")

	key := resolveAnthropicKey("ANTHROPIC_API_KEY")
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

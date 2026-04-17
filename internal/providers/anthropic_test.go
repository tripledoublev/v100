package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestAnthropicConvertMessagesWithImage(t *testing.T) {
	msgs := []Message{
		{
			Role:    "user",
			Content: "What is in this image?",
			Images: []ImageAttachment{{
				MIMEType: "image/png",
				Data:     []byte{0x89, 0x50, 0x4e, 0x47},
			}},
		},
	}

	_, converted := anthropicConvertMessages(msgs)
	if len(converted) != 1 {
		t.Fatalf("expected 1 converted message, got %d", len(converted))
	}
	blocks, ok := converted[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("expected content blocks, got %T", converted[0].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected text and image blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "What is in this image?" {
		t.Fatalf("unexpected text block: %#v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("unexpected image block: %#v", blocks[1])
	}
	if blocks[1].Source.Type != "base64" || blocks[1].Source.MediaType != "image/png" {
		t.Fatalf("unexpected image source metadata: %#v", blocks[1].Source)
	}
}

// TestEnsureToolResultContiguity_InterleavedUserMessage covers the MiniMax
// error 2013 scenario: a user message appears between an assistant tool_use
// and its tool results (e.g. from a budget-alert injection).
func TestEnsureToolResultContiguity_InterleavedUserMessage(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "fs_read", Args: json.RawMessage(`{}`)},
		}},
		{Role: "user", Content: "budget alert: 90% used"}, // interleaved
		{Role: "tool", Content: "file contents", ToolCallID: "tc1"},
	}

	reordered := ensureToolResultContiguity(msgs)

	// Find positions
	var assistantIdx, toolResultIdx, userAlertIdx int
	for i, m := range reordered {
		switch {
		case m.Role == "assistant":
			assistantIdx = i
		case m.Role == "tool":
			toolResultIdx = i
		case m.Role == "user" && m.Content == "budget alert: 90% used":
			userAlertIdx = i
		}
	}

	if toolResultIdx != assistantIdx+1 {
		t.Errorf("tool result should immediately follow assistant (got assistant=%d, tool=%d)", assistantIdx, toolResultIdx)
	}
	if userAlertIdx <= toolResultIdx {
		t.Errorf("deferred user message should come after tool result (got tool=%d, user=%d)", toolResultIdx, userAlertIdx)
	}
}

// TestEnsureToolResultContiguity_AlreadyOrdered verifies no reordering when messages are already correct.
func TestEnsureToolResultContiguity_AlreadyOrdered(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "fs_read", Args: json.RawMessage(`{}`)},
			{ID: "tc2", Name: "fs_read", Args: json.RawMessage(`{}`)},
		}},
		{Role: "tool", Content: "a", ToolCallID: "tc1"},
		{Role: "tool", Content: "b", ToolCallID: "tc2"},
	}

	reordered := ensureToolResultContiguity(msgs)
	if len(reordered) != len(msgs) {
		t.Fatalf("expected same length, got %d", len(reordered))
	}
	for i, m := range reordered {
		if m.Role != msgs[i].Role {
			t.Errorf("pos %d: expected role %s, got %s", i, msgs[i].Role, m.Role)
		}
	}
}

func TestSanitizeToolHistoryDropsUnresolvedAssistantToolCall(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "download"},
		{Role: "assistant", Content: "starting", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "sh", Args: json.RawMessage(`{"cmd":"sleep 60"}`)},
		}},
		{Role: "user", Content: "continue"},
		{Role: "assistant", Content: "I can continue without that result."},
	}

	got := sanitizeToolHistory(msgs)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages after sanitizing unresolved tool call, got %d", len(got))
	}
	if len(got[1].ToolCalls) != 0 {
		t.Fatalf("expected unresolved tool call to be dropped, got %+v", got[1].ToolCalls)
	}
	if got[1].Content != "starting" {
		t.Fatalf("expected assistant text to be preserved, got %q", got[1].Content)
	}
}

func TestSanitizeToolHistoryDropsOrphanedToolResult(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: "late result", ToolCallID: "tc-missing", Name: "sh"},
		{Role: "assistant", Content: "done"},
	}

	got := sanitizeToolHistory(msgs)
	if len(got) != 2 {
		t.Fatalf("expected orphaned tool result to be dropped, got %d messages", len(got))
	}
	for _, msg := range got {
		if msg.Role == "tool" {
			t.Fatalf("unexpected tool message after sanitizing: %+v", msg)
		}
	}
}

func TestAnthropicConvertMessagesDropsUnresolvedToolUse(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "working", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "sh", Args: json.RawMessage(`{"cmd":"sleep 60"}`)},
		}},
		{Role: "user", Content: "continue"},
	}

	_, converted := anthropicConvertMessages(msgs)
	if len(converted) != 3 {
		t.Fatalf("expected unresolved tool_use to be omitted, got %d messages", len(converted))
	}
	if converted[1].Role != "assistant" || converted[1].Content != "working" {
		t.Fatalf("expected text-only assistant message, got role=%q content=%#v", converted[1].Role, converted[1].Content)
	}
	if converted[2].Role != "user" {
		t.Fatalf("expected trailing user message to remain, got %q", converted[2].Role)
	}
}

func TestAnthropicConvertMessagesNormalizesEmptyToolInput(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "show status"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "git_status"},
		}},
		{Role: "tool", Content: "clean", ToolCallID: "tc1", Name: "git_status"},
	}

	_, converted := anthropicConvertMessages(msgs)
	if len(converted) < 2 {
		t.Fatalf("expected converted tool call message, got %d messages", len(converted))
	}
	blocks, ok := converted[1].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("expected content blocks, got %T", converted[1].Content)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 tool_use block, got %d", len(blocks))
	}
	if string(blocks[0].Input) != "{}" {
		t.Fatalf("expected empty tool input to normalize to {}, got %q", string(blocks[0].Input))
	}
}

func TestAnthropicBuildRequestKeepsInputForMixedToolArgs(t *testing.T) {
	req := CompleteRequest{
		Model: "claude-opus-4-7",
		Messages: []Message{
			{Role: "user", Content: "show staged diff and status"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "tc-diff", Name: "git_diff", Args: json.RawMessage(`{"staged":true}`)},
				{ID: "tc-status", Name: "git_status"},
			}},
			{Role: "tool", Content: "diff", ToolCallID: "tc-diff", Name: "git_diff"},
			{Role: "tool", Content: "status", ToolCallID: "tc-status", Name: "git_status"},
		},
	}

	aReq := anthropicBuildRequest(req.Model, req)
	body, err := json.Marshal(aReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	raw := string(body)
	if !strings.Contains(raw, `"name":"git_status","input":{}`) {
		t.Fatalf("expected git_status tool_use to include empty input object, got %s", raw)
	}
	if !strings.Contains(raw, `"name":"git_diff","input":{"staged":true}`) {
		t.Fatalf("expected git_diff tool_use to retain explicit args, got %s", raw)
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

func TestAnthropicModelsIncludesClaudeAliasDefault(t *testing.T) {
	p := &AnthropicProvider{defaultModel: claudeDefaultModel}
	models := p.Models()
	if len(models) == 0 {
		t.Fatal("expected anthropic model list")
	}
	if models[0].Name != claudeDefaultModel {
		t.Fatalf("expected first model %q, got %q", claudeDefaultModel, models[0].Name)
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

	costFn := func(input, output int) float64 {
		return anthropicEstimateCost("claude-sonnet-4-20250514", input, output)
	}
	resp, err := anthropicParseResponse([]byte(raw), costFn)
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

func TestExtractTextualToolCalls_MinimaxEnvelope(t *testing.T) {
	// This is the exact payload the user saw leaking into the TUI.
	input := "let me check\n<minimax:tool_call>\n<invoke name=\"fs_read\">\n<parameter name=\"end_line\">280</parameter>\n<parameter name=\"path\">/workspace/internal/ui/events.go</parameter>\n<parameter name=\"start_line\">255</parameter>\n</invoke>\n</minimax:tool_call>\ndone"

	cleaned, calls := ExtractTextualToolCalls(input)

	if strings.Contains(cleaned, "<minimax:tool_call>") || strings.Contains(cleaned, "<invoke") {
		t.Fatalf("cleaned text still contains tool-call markup: %q", cleaned)
	}
	if !strings.Contains(cleaned, "let me check") || !strings.Contains(cleaned, "done") {
		t.Errorf("surrounding prose lost: %q", cleaned)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "fs_read" {
		t.Errorf("name=%q, want fs_read", calls[0].Name)
	}
	var args map[string]string
	if err := json.Unmarshal(calls[0].Args, &args); err != nil {
		t.Fatalf("args not valid JSON: %v", err)
	}
	if args["path"] != "/workspace/internal/ui/events.go" || args["start_line"] != "255" || args["end_line"] != "280" {
		t.Errorf("args mis-parsed: %+v", args)
	}
	if !strings.HasPrefix(calls[0].ID, "txt_") {
		t.Errorf("expected synthesized id with txt_ prefix, got %q", calls[0].ID)
	}
}

func TestExtractTextualToolCalls_NoMarkup(t *testing.T) {
	in := "just plain text, no tool calls here"
	out, calls := ExtractTextualToolCalls(in)
	if out != in {
		t.Errorf("plain text mutated: %q", out)
	}
	if len(calls) != 0 {
		t.Errorf("unexpected calls: %+v", calls)
	}
}

func TestExtractTextualToolCalls_MultipleInvokes(t *testing.T) {
	in := "<minimax:tool_call>\n<invoke name=\"a\"><parameter name=\"x\">1</parameter></invoke>\n<invoke name=\"b\"><parameter name=\"y\">2</parameter></invoke>\n</minimax:tool_call>"
	_, calls := ExtractTextualToolCalls(in)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Name != "a" || calls[1].Name != "b" {
		t.Errorf("wrong order/names: %+v", calls)
	}
}

func TestAnthropicParseResponse_ExtractsTextualToolCalls(t *testing.T) {
	body := `{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"<minimax:tool_call>\n<invoke name=\"sh\">\n<parameter name=\"cmd\">ls</parameter>\n</invoke>\n</minimax:tool_call>"}],"usage":{"input_tokens":10,"output_tokens":20},"stop_reason":"end_turn"}`
	resp, err := anthropicParseResponse([]byte(body), func(_, _ int) float64 { return 0 })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Contains(resp.AssistantText, "<invoke") {
		t.Errorf("assistant text leaks markup: %q", resp.AssistantText)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "sh" {
		t.Fatalf("tool calls not extracted: %+v", resp.ToolCalls)
	}
}

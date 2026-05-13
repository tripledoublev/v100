package core

import (
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

func TestPressureMonitorNoPressure(t *testing.T) {
	hook := PressureMonitor(0.70)

	// No context size info — should continue
	state := LoopState{ContextPressure: 0, ContextWindowSize: 0}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue with no context info, got %v", res.Action)
	}
}

func TestPressureMonitorBelowThreshold(t *testing.T) {
	hook := PressureMonitor(0.70)

	state := LoopState{ContextPressure: 0.50, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue below threshold, got %v", res.Action)
	}
}

func TestPressureMonitorWarnOnFirstBreach(t *testing.T) {
	hook := PressureMonitor(0.70)

	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}
	if res.Reason != "context_pressure_warn" {
		t.Fatalf("expected reason context_pressure_warn, got %s", res.Reason)
	}
}

func TestPressureMonitorSustainedHighForcesReplan(t *testing.T) {
	hook := PressureMonitor(0.70)

	// First breach: warn
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}

	// Sustained high pressure (above threshold * 1.15 = 0.805): force replan
	state = LoopState{ContextPressure: 0.85, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookForceReplan {
		t.Fatalf("expected HookForceReplan at sustained high pressure, got %v", res.Action)
	}
}

func TestPressureMonitorResetsAfterDrop(t *testing.T) {
	hook := PressureMonitor(0.70)

	// First breach
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}

	// Drop below threshold — should reset
	state = LoopState{ContextPressure: 0.50, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue after pressure drops, got %v", res.Action)
	}

	// Breach again — should warn again (not replan)
	state = LoopState{ContextPressure: 0.72, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on re-breach after reset, got %v", res.Action)
	}
}

func TestPressureMonitorDefaultThreshold(t *testing.T) {
	// threshold=0 should default to 0.70
	hook := PressureMonitor(0)

	// Below 0.70: continue
	state := LoopState{ContextPressure: 0.60, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue below default threshold, got %v", res.Action)
	}

	// At 0.75: warn
	state = LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage above default threshold, got %v", res.Action)
	}
}

func TestPressureMonitorCustomThreshold(t *testing.T) {
	hook := PressureMonitor(0.50)

	// At 0.55: warn (above 0.50 threshold)
	state := LoopState{ContextPressure: 0.55, ContextWindowSize: 100000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage with custom threshold, got %v", res.Action)
	}
}

func TestEstimateTokensFromState(t *testing.T) {
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 100000}
	est := estimateTokensFromState(state)
	if est != 75000 {
		t.Fatalf("expected 75000, got %d", est)
	}

	// Zero values
	state = LoopState{}
	est = estimateTokensFromState(state)
	if est != 0 {
		t.Fatalf("expected 0 for empty state, got %d", est)
	}
}

func TestCharsToTokens(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 0},
		{-1, 0},
		{1, 1},    // (10+32)/33 = 1
		{3, 1},    // (30+32)/33 = 1
		{4, 2},    // (40+32)/33 = 2 → actually (72)/33 = 2
		{33, 10},  // (330+32)/33 = 10
		{100, 31}, // (1000+32)/33 = 31
		{330, 100},// (3300+32)/33 = 101 → hmm, let me recalc: 3332/33 = 100.96 → 100
	}
	for _, tt := range tests {
		got := charsToTokens(tt.input)
		if got != tt.want {
			t.Errorf("charsToTokens(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEstimateTokensSingle(t *testing.T) {
	// Basic user message with content only.
	m := providers.Message{Role: "user", Content: "Hello, world!"}
	est := estimateTokensSingle(m)
	// Role "user" = 4 bytes → 1 token. Content = 13 bytes → (130+32)/33 = 4. Framing = 4.
	// Total: 4 + 1 + 4 = 9
	if est == 0 {
		t.Fatal("expected non-zero token estimate for user message")
	}
	t.Logf("user message estimate: %d", est)

	// Tool result message with ToolCallID and Name.
	m = providers.Message{
		Role:       "tool",
		Content:    `{"result": "ok"}`,
		ToolCallID: "call_abc123",
		Name:       "read_file",
	}
	est = estimateTokensSingle(m)
	if est == 0 {
		t.Fatal("expected non-zero token estimate for tool message")
	}
	t.Logf("tool result estimate: %d", est)

	// Assistant message with tool calls.
	m = providers.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{ID: "call_001", Name: "shell", Args: []byte(`{"cmd":"ls -la"}`)},
			{ID: "call_002", Name: "read_file", Args: []byte(`{"path":"/etc/hosts"}`)},
		},
	}
	est = estimateTokensSingle(m)
	if est == 0 {
		t.Fatal("expected non-zero token estimate for tool-calling message")
	}
	// Should account for both tool calls.
	if est < 10 {
		t.Fatalf("expected at least 10 tokens for tool-calling message, got %d", est)
	}
	t.Logf("assistant with 2 tool calls estimate: %d", est)

	// Message with image attachments.
	m = providers.Message{
		Role:    "user",
		Content: "What do you see?",
		Images: []providers.ImageAttachment{
			{MIMEType: "image/png", Data: make([]byte, 1000)},
			{MIMEType: "image/jpeg", Data: make([]byte, 5000)},
		},
	}
	est = estimateTokensSingle(m)
	// 2 images × 85 tokens = 170 tokens from images alone.
	if est < 170 {
		t.Fatalf("expected at least 170 tokens with 2 images, got %d", est)
	}
	t.Logf("message with 2 images estimate: %d", est)

	// Empty message should still have framing overhead.
	m = providers.Message{Role: "system", Content: ""}
	est = estimateTokensSingle(m)
	if est < 4 {
		t.Fatalf("expected at least framing overhead (4) for empty message, got %d", est)
	}
}

func TestEstimateTokens(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hello!"},
		{Role: "assistant", Content: "Hi there! How can I help you today?"},
	}
	est := EstimateTokens(msgs)
	if est == 0 {
		t.Fatal("expected non-zero total token estimate")
	}

	// Sum of individual estimates should match.
	sum := 0
	for _, m := range msgs {
		sum += estimateTokensSingle(m)
	}
	if est != sum {
		t.Fatalf("EstimateTokens = %d, sum of individual = %d", est, sum)
	}

	// Empty slice should return 0.
	est = EstimateTokens(nil)
	if est != 0 {
		t.Fatalf("expected 0 for nil slice, got %d", est)
	}
}

func TestEstimateTokensConsistency(t *testing.T) {
	// Verify that the ceiling-division formula is consistent: for a given string,
	// the result should be >= floor(len/3.3) and <= ceil(len/3.3).
	for _, length := range []int{1, 10, 33, 50, 100, 500, 1000} {
		tokens := charsToTokens(length)
		lower := float64(length) / 3.3
		if float64(tokens) < lower-1 || float64(tokens) > lower+1 {
			t.Errorf("charsToTokens(%d) = %d, expected near %.1f", length, tokens, lower)
		}
	}
}

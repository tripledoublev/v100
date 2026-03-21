package core

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestToolResultCharLimitUsesProviderContextHeadroom(t *testing.T) {
	l := &Loop{
		Policy: &policy.Policy{MaxToolResultChars: 20000},
		ModelMetadata: providers.ModelMetadata{
			ContextSize: 4096,
		},
		Messages: []providers.Message{
			{Role: "user", Content: strings.Repeat("u", 9000)},
			{Role: "assistant", Content: "working"},
		},
	}

	got := l.toolResultCharLimit("step-1")
	if got <= 0 || got >= 20000 {
		t.Fatalf("expected dynamic limit smaller than static limit, got %d", got)
	}
	if got != 400 {
		t.Fatalf("expected saturated context to clamp to minimum dynamic limit 400, got %d", got)
	}
}

func TestToolResultCharLimitFallsBackToStaticWhenNoContextMetadata(t *testing.T) {
	l := &Loop{
		Policy: &policy.Policy{MaxToolResultChars: 1234},
	}

	if got := l.toolResultCharLimit("step-1"); got != 1234 {
		t.Fatalf("toolResultCharLimit = %d, want 1234", got)
	}
}

func TestToolResultCharLimitUsesPolicyContextLimit(t *testing.T) {
	l := &Loop{
		Policy: &policy.Policy{
			ContextLimit:       8000,
			MaxToolResultChars: 20000,
		},
		Messages: []providers.Message{
			{Role: "user", Content: strings.Repeat("u", 5000)},
		},
	}

	got := l.toolResultCharLimit("step-1")
	if got <= 0 || got >= 20000 {
		t.Fatalf("expected policy-based dynamic limit smaller than static limit, got %d", got)
	}
}

func TestTruncateToolResultWithDynamicLimit(t *testing.T) {
	content := strings.Repeat("A", 2000)
	got := TruncateToolResult(content, 400)
	if len(got) >= len(content) {
		t.Fatalf("expected dynamic truncation to shorten content, got %d", len(got))
	}
	if !strings.Contains(got, "[... truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

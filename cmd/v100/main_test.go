package main

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

func TestIsCompliantAgentHandoff(t *testing.T) {
	valid := strings.Repeat("x", 90) + `
## Summary
Short summary.

## Findings
- [P1] Something important.

## Next Steps
1. Do thing.
`
	if !isCompliantAgentHandoff(valid) {
		t.Fatalf("expected valid handoff to be compliant")
	}

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "too short", in: "## Summary\nx\n## Findings\nx\n## Next Steps\nx"},
		{name: "missing findings", in: strings.Repeat("x", 100) + "\n## Summary\nx\n## Next Steps\n1. x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isCompliantAgentHandoff(tc.in) {
				t.Fatalf("expected non-compliant handoff for case %q", tc.name)
			}
		})
	}
}

func TestBuildSubAgentTask(t *testing.T) {
	first := buildSubAgentTask("analyze codebase", "", 1)
	if !strings.Contains(first, "analyze codebase") {
		t.Fatalf("first attempt prompt missing task")
	}
	if strings.Contains(first, "Your previous response was not compliant or empty.") {
		t.Fatalf("first attempt prompt should not include retry guidance")
	}
	if !strings.Contains(first, "## Summary") || !strings.Contains(first, "## Findings") || !strings.Contains(first, "## Next Steps") {
		t.Fatalf("first attempt prompt missing output contract")
	}

	second := buildSubAgentTask("analyze codebase", "bad output", 2)
	if !strings.Contains(second, "Your previous response was not compliant or empty.") {
		t.Fatalf("retry prompt missing retry guidance")
	}
	if !strings.Contains(second, "Previous output:\nbad output") {
		t.Fatalf("retry prompt missing previous output context")
	}
}

func TestExtractLastAssistantText(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first"},
		{Role: "assistant", Content: "   "},
		{Role: "tool", Content: "ignored"},
		{Role: "assistant", Content: "last answer"},
	}
	got := extractLastAssistantText(msgs)
	if got != "last answer" {
		t.Fatalf("unexpected last assistant text: %q", got)
	}

	if v := extractLastAssistantText(nil); v != "" {
		t.Fatalf("expected empty from nil messages, got %q", v)
	}
}

func TestParseInjectedToolOutputs(t *testing.T) {
	m, err := parseInjectedToolOutputs([]string{"project_search=parser.go:42", "fs_read=mocked file"})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := m["project_search"]; got != "parser.go:42" {
		t.Fatalf("unexpected project_search value: %q", got)
	}
	if got := m["fs_read"]; got != "mocked file" {
		t.Fatalf("unexpected fs_read value: %q", got)
	}

	if _, err := parseInjectedToolOutputs([]string{"bad-format"}); err == nil {
		t.Fatalf("expected parse error for missing '='")
	}
}

func TestApplyInjectedToolOutputs(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "find parser"},
		{Role: "tool", Name: "project_search", Content: "old"},
		{Role: "tool", Name: "fs_read", Content: "keep"},
	}
	injected := map[string]string{"project_search": "new-value"}
	got := applyInjectedToolOutputs(msgs, injected)
	if got[1].Content != "new-value" {
		t.Fatalf("expected injected tool content, got %q", got[1].Content)
	}
	if got[2].Content != "keep" {
		t.Fatalf("expected untouched tool content, got %q", got[2].Content)
	}
	if msgs[1].Content != "old" {
		t.Fatalf("input slice should not be mutated")
	}
}

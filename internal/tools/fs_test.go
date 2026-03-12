package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFSWriteOutputIncludesDigestAndPreview(t *testing.T) {
	dir := t.TempDir()
	tool := FSWrite()
	ctx := context.Background()
	call := ToolCallContext{Mapper: &MockMapper{Dir: dir}}

	content := "hello world"
	args, _ := json.Marshal(map[string]string{"path": "test.txt", "content": content})

	result, err := tool.Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"sha256":`) {
		t.Errorf("output missing sha256: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"preview":`) {
		t.Errorf("output missing preview: %s", result.Output)
	}
	if !strings.Contains(result.Output, "hello world") {
		t.Errorf("preview should contain content: %s", result.Output)
	}
}

func TestFSWritePreviewTruncatesLongContent(t *testing.T) {
	dir := t.TempDir()
	tool := FSWrite()
	ctx := context.Background()
	call := ToolCallContext{Mapper: &MockMapper{Dir: dir}}

	content := strings.Repeat("x", 300)
	args, _ := json.Marshal(map[string]string{"path": "big.txt", "content": content})

	result, err := tool.Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	// Preview should be truncated — full 300-char content should not appear
	if strings.Contains(result.Output, content) {
		t.Error("preview should truncate content longer than 200 chars")
	}
}

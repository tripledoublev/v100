package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type virtualizingMapper struct {
	Dir string
}

func (m *virtualizingMapper) ToSandbox(path string) string {
	return filepath.Join(m.Dir, strings.TrimPrefix(path, "/"))
}

func (m *virtualizingMapper) ToVirtual(path string) string {
	rel, err := filepath.Rel(m.Dir, path)
	if err != nil || rel == "." {
		return "/workspace"
	}
	return filepath.ToSlash(filepath.Join("/workspace", rel))
}

func (m *virtualizingMapper) SanitizeText(text string) string {
	return text
}

func (m *virtualizingMapper) SecurePath(path string) (string, bool) {
	return m.ToSandbox(path), true
}

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

func TestFSWriteWarnsWhenAbsoluteTmpPathIsRemapped(t *testing.T) {
	dir := t.TempDir()
	tool := FSWrite()
	ctx := context.Background()
	call := ToolCallContext{Mapper: &virtualizingMapper{Dir: dir}}

	args, _ := json.Marshal(map[string]string{"path": "/tmp/foo.txt", "content": "hello"})
	result, err := tool.Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"warning":`) {
		t.Fatalf("expected warning in output, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, `/workspace/tmp/foo.txt`) {
		t.Fatalf("expected remapped sandbox path in warning, got: %s", result.Output)
	}
}

func TestFSReadWarnsWhenAbsoluteTmpPathIsRemapped(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tmp", "foo.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := FSRead()
	ctx := context.Background()
	call := ToolCallContext{Mapper: &virtualizingMapper{Dir: dir}}

	args, _ := json.Marshal(map[string]string{"path": "/tmp/foo.txt"})
	result, err := tool.Exec(ctx, call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("expected OK, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "[warning]") {
		t.Fatalf("expected warning prefix, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "/workspace/tmp/foo.txt") {
		t.Fatalf("expected remapped sandbox path in warning, got: %s", result.Output)
	}
}

package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewMiniMaxProviderLoadToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	tokenPath := filepath.Join(dir, "v100", "minimax_auth.json")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		t.Fatal(err)
	}

	tok := map[string]any{
		"access":     "test-access-token",
		"refresh":    "test-refresh-token",
		"expires_ms": time.Now().UnixMilli() + 3600_000,
	}
	data, _ := json.Marshal(tok)
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewMiniMaxProvider(tokenPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Name() != "minimax" {
		t.Errorf("expected name minimax, got %s", p.Name())
	}
	if p.defaultModel != minimaxDefaultModel {
		t.Errorf("expected default model %s, got %s", minimaxDefaultModel, p.defaultModel)
	}
}

func TestNewMiniMaxProviderCustomModel(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "minimax_auth.json")
	tok := map[string]any{
		"access":     "tok",
		"refresh":    "ref",
		"expires_ms": time.Now().UnixMilli() + 3600_000,
	}
	data, _ := json.Marshal(tok)
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewMiniMaxProvider(tokenPath, "custom-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.defaultModel != "custom-model" {
		t.Errorf("expected custom-model, got %s", p.defaultModel)
	}
}

func TestNewMiniMaxProviderNoToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "nonexistent.json")

	_, err := NewMiniMaxProvider(tokenPath, "")
	if err == nil {
		t.Fatal("expected error for missing token file")
	}
}

func TestMiniMaxCapabilities(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "minimax_auth.json")
	tok := map[string]any{
		"access":     "tok",
		"refresh":    "ref",
		"expires_ms": time.Now().UnixMilli() + 3600_000,
	}
	data, _ := json.Marshal(tok)
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewMiniMaxProvider(tokenPath, "")
	if err != nil {
		t.Fatal(err)
	}

	caps := p.Capabilities()
	if !caps.ToolCalls {
		t.Error("expected ToolCalls=true")
	}
	if !caps.Streaming {
		t.Error("expected Streaming=true")
	}
	if !caps.Images {
		t.Error("expected Images=true for the default model (MiniMax-M3, which is vision-capable)")
	}

	m27, err := NewMiniMaxProvider(tokenPath, "MiniMax-M2.7")
	if err != nil {
		t.Fatal(err)
	}
	if m27.Capabilities().Images {
		t.Error("expected Images=false for MiniMax-M2.7: coding-plan models don't accept image input")
	}
}

func TestIsMiniMaxVisionModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"MiniMax-M3", true},
		{"minimax-m3", true},
		{"  MiniMax-M3  ", true},
		{"MiniMax-M3-vision", true},
		{"MiniMax-M30", false},
		{"MiniMax-M2.7", false},
		{"MiniMax-M2.7-highspeed", false},
		{"MiniMax-M2.5", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isMiniMaxVisionModel(tc.model); got != tc.want {
			t.Errorf("isMiniMaxVisionModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestMiniMaxMetadata(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "minimax_auth.json")
	tok := map[string]any{
		"access":     "tok",
		"refresh":    "ref",
		"expires_ms": time.Now().UnixMilli() + 3600_000,
	}
	data, _ := json.Marshal(tok)
	if err := os.WriteFile(tokenPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewMiniMaxProvider(tokenPath, "")
	if err != nil {
		t.Fatal(err)
	}

	meta, err := p.Metadata(context.TODO(), "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Model != minimaxDefaultModel {
		t.Errorf("expected model %s, got %s", minimaxDefaultModel, meta.Model)
	}
	if meta.ContextSize != 200000 {
		t.Errorf("expected context size 200000, got %d", meta.ContextSize)
	}
	if !meta.IsFree {
		t.Error("expected IsFree=true")
	}
}

func TestMiniMaxUsesAnthropicFormat(t *testing.T) {
	// Verify that anthropicBuildRequest produces valid output for MiniMax
	req := CompleteRequest{
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []ToolSpec{
			{
				Name:        "fs_read",
				Description: "Read a file",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
		GenParams: GenParams{MaxTokens: 1024},
	}

	aReq := anthropicBuildRequest("MiniMax-M2.7", req)

	if aReq.Model != "MiniMax-M2.7" {
		t.Errorf("expected model MiniMax-M2.7, got %s", aReq.Model)
	}
	if aReq.System != "You are helpful." {
		t.Errorf("expected system prompt, got %q", aReq.System)
	}
	if len(aReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(aReq.Messages))
	}
	if aReq.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %d", aReq.MaxTokens)
	}
	if len(aReq.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(aReq.Tools))
	}
	if aReq.Tools[0].Name != "fs_read" {
		t.Errorf("expected tool fs_read, got %s", aReq.Tools[0].Name)
	}
}

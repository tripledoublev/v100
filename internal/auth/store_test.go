package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStoresSaveLoadAndValidity(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()

	codexPath := filepath.Join(dir, "auth.json")
	if err := Save(codexPath, &Token{Access: "access", Refresh: "refresh", ExpiresMS: future, AccountID: "acct"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	codex, err := Load(codexPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !codex.Valid() || codex.AccountID != "acct" {
		t.Fatalf("unexpected codex token: %+v", codex)
	}
	if (&Token{Access: "access", ExpiresMS: past}).Valid() {
		t.Fatal("expired codex token should be invalid")
	}

	geminiPath := filepath.Join(dir, "gemini_auth.json")
	if err := SaveGemini(geminiPath, &GeminiToken{Access: "g-access", Refresh: "g-refresh", ExpiresMS: future, ProjectID: "project"}); err != nil {
		t.Fatalf("SaveGemini() error = %v", err)
	}
	gemini, err := LoadGemini(geminiPath)
	if err != nil {
		t.Fatalf("LoadGemini() error = %v", err)
	}
	if !gemini.Valid() || gemini.ProjectID != "project" {
		t.Fatalf("unexpected gemini token: %+v", gemini)
	}
	if (&GeminiToken{Access: "access", ExpiresMS: past}).Valid() {
		t.Fatal("expired gemini token should be invalid")
	}

	minimaxPath := filepath.Join(dir, "minimax_auth.json")
	if err := SaveMiniMax(minimaxPath, &MiniMaxToken{Access: "m-access", Refresh: "m-refresh", ExpiresMS: future}); err != nil {
		t.Fatalf("SaveMiniMax() error = %v", err)
	}
	minimax, err := LoadMiniMax(minimaxPath)
	if err != nil {
		t.Fatalf("LoadMiniMax() error = %v", err)
	}
	if !minimax.Valid() || minimax.Refresh != "m-refresh" {
		t.Fatalf("unexpected minimax token: %+v", minimax)
	}
	if (&MiniMaxToken{Access: "access", ExpiresMS: past}).Valid() {
		t.Fatal("expired minimax token should be invalid")
	}
}

func TestDefaultTokenPathsRespectXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/v100-xdg")
	want := map[string]string{
		DefaultTokenPath():        "/tmp/v100-xdg/v100/auth.json",
		DefaultGeminiTokenPath():  "/tmp/v100-xdg/v100/gemini_auth.json",
		DefaultClaudeTokenPath():  "/tmp/v100-xdg/v100/anthropic_auth.json",
		DefaultMiniMaxTokenPath(): "/tmp/v100-xdg/v100/minimax_auth.json",
	}
	for got, expected := range want {
		if got != expected {
			t.Fatalf("path = %q, want %q", got, expected)
		}
	}
}

func TestDefaultTokenPathsUseHomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/v100-home")
	for _, got := range []string{
		DefaultTokenPath(),
		DefaultGeminiTokenPath(),
		DefaultClaudeTokenPath(),
		DefaultMiniMaxTokenPath(),
		DefaultCredentialsPath(),
	} {
		if !strings.HasPrefix(got, "/tmp/v100-home/.config/v100/") {
			t.Fatalf("path = %q, want home fallback", got)
		}
	}
}

func TestLoadClaudeEnvSecretAndPlaintextOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anthropic_auth.json")
	if err := os.WriteFile(path, []byte(`{"api_key":"file-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	withSecretManagers(t, fakeSecretManager{values: map[string]string{"provider_anthropic_api_key": "secret-key"}})
	token, err := LoadClaude(path)
	if err != nil {
		t.Fatalf("LoadClaude() env error = %v", err)
	}
	if token.APIKey != "env-key" {
		t.Fatalf("env API key = %q", token.APIKey)
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	token, err = LoadClaude(path)
	if err != nil {
		t.Fatalf("LoadClaude() secret error = %v", err)
	}
	if token.APIKey != "secret-key" {
		t.Fatalf("secret API key = %q", token.APIKey)
	}

	withSecretManagers(t)
	warnings := capturePlaintextWarnings(t)
	token, err = LoadClaude(path)
	if err != nil {
		t.Fatalf("LoadClaude() plaintext error = %v", err)
	}
	if token.APIKey != "file-key" {
		t.Fatalf("file API key = %q", token.APIKey)
	}
	if !strings.Contains(warnings.String(), "plaintext Anthropic API key") {
		t.Fatalf("expected warning, got %q", warnings.String())
	}
}

package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialsTemplateIsValidJSON(t *testing.T) {
	var creds OAuthCredentials
	if err := json.Unmarshal([]byte(CredentialsTemplate()), &creds); err != nil {
		t.Fatalf("CredentialsTemplate() returned invalid JSON: %v", err)
	}
}

func TestLoadCodexCredentialsRequiresField(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"gemini_client_id":"gid","gemini_client_secret":"gsecret"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	_, err := LoadCodexCredentials()
	if err == nil {
		t.Fatal("LoadCodexCredentials() returned nil error")
	}
	if !strings.Contains(err.Error(), "codex_client_id") {
		t.Fatalf("expected codex_client_id error, got %v", err)
	}
}

func TestLoadGeminiCredentialsRequiresFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"codex_client_id":"cid","gemini_client_id":"gid"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	_, err := LoadGeminiCredentials()
	if err == nil {
		t.Fatal("LoadGeminiCredentials() returned nil error")
	}
	if !strings.Contains(err.Error(), "gemini_client_secret") {
		t.Fatalf("expected gemini_client_secret error, got %v", err)
	}
}

func TestClaudeTokenSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anthropic_auth.json")

	ct := &ClaudeToken{APIKey: "sk-ant-api03-test-key"}
	if err := SaveClaude(path, ct); err != nil {
		t.Fatalf("SaveClaude() error = %v", err)
	}

	loaded, err := LoadClaude(path)
	if err != nil {
		t.Fatalf("LoadClaude() error = %v", err)
	}
	if loaded.APIKey != ct.APIKey {
		t.Errorf("expected %s, got %s", ct.APIKey, loaded.APIKey)
	}
	if !loaded.Valid() {
		t.Error("expected Valid() = true")
	}
}

func TestClaudeTokenValidEmpty(t *testing.T) {
	ct := &ClaudeToken{}
	if ct.Valid() {
		t.Error("expected Valid() = false for empty key")
	}
	var nilToken *ClaudeToken
	if nilToken.Valid() {
		t.Error("expected Valid() = false for nil token")
	}
}

func TestClaudeTokenPathXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	path := DefaultClaudeTokenPath()
	if path != "/tmp/xdg-test/v100/anthropic_auth.json" {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestLoadGeminiCredentialsSucceeds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"codex_client_id":"cid","gemini_client_id":"gid","gemini_client_secret":"gsecret"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	creds, err := LoadGeminiCredentials()
	if err != nil {
		t.Fatalf("LoadGeminiCredentials() error = %v", err)
	}
	if creds.GeminiClientID != "gid" || creds.GeminiClientSecret != "gsecret" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
}

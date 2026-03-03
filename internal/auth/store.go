package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Token holds OAuth credentials for the Codex provider.
type Token struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	ExpiresMS int64  `json:"expires_ms"`
	AccountID string `json:"account_id"`
}

// Valid reports whether the token is usable (not expiring within 60 s).
func (t *Token) Valid() bool {
	return t != nil && t.Access != "" && time.Now().UnixMilli()+60_000 < t.ExpiresMS
}

// DefaultTokenPath returns ~/.config/v100/auth.json.
func DefaultTokenPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "v100", "auth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "v100", "auth.json")
}

// Load reads a Token from path.
func Load(path string) (*Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read %s: %w", path, err)
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("auth: parse %s: %w", path, err)
	}
	return &t, nil
}

// Save writes a Token to path (creates parent directories as needed).
func Save(path string, t *Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("auth: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("auth: write %s: %w", path, err)
	}
	return nil
}

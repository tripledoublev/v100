package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCredentialsTemplateIsValidJSON(t *testing.T) {
	var creds OAuthCredentials
	if err := json.Unmarshal([]byte(CredentialsTemplate()), &creds); err != nil {
		t.Fatalf("CredentialsTemplate() returned invalid JSON: %v", err)
	}
}

func TestLoadCodexCredentialsRequiresField(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withSecretManagers(t)
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
	withSecretManagers(t)
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
	withSecretManagers(t)
	warnings := capturePlaintextWarnings(t)
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
	if !strings.Contains(warnings.String(), "plaintext Anthropic API key") {
		t.Fatalf("expected plaintext warning, got %q", warnings.String())
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
	withSecretManagers(t)
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

func TestLoadCodexCredentialsUsesEnvBeforePlaintext(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("V100_CODEX_CLIENT_ID", "env-cid")
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"codex_client_id":"file-cid"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	creds, err := LoadCodexCredentials()
	if err != nil {
		t.Fatalf("LoadCodexCredentials() error = %v", err)
	}
	if creds.CodexClientID != "env-cid" {
		t.Fatalf("codex client ID = %q, want env-cid", creds.CodexClientID)
	}
}

func TestLoadGeminiCredentialsUsesSecretThenPlaintextFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withSecretManagers(t, fakeSecretManager{
		name:   "test-secrets",
		values: map[string]string{"oauth_gemini_client_id": "secret-gid"},
	})
	warnings := capturePlaintextWarnings(t)
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"gemini_client_secret":"file-secret"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	creds, err := LoadGeminiCredentials()
	if err != nil {
		t.Fatalf("LoadGeminiCredentials() error = %v", err)
	}
	if creds.GeminiClientID != "secret-gid" || creds.GeminiClientSecret != "file-secret" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
	if !strings.Contains(warnings.String(), "plaintext OAuth credentials") {
		t.Fatalf("expected plaintext warning, got %q", warnings.String())
	}
}

func TestLoadGeminiCredentialsMissingErrorNamesEnvSecretsAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withSecretManagers(t)

	_, err := LoadGeminiCredentials()
	if err == nil {
		t.Fatal("LoadGeminiCredentials() returned nil error")
	}
	for _, want := range []string{"V100_GEMINI_CLIENT_ID", "oauth_gemini_client_secret", DefaultCredentialsPath()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestLoadMiniMaxCredentialsEnvAndDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withSecretManagers(t)

	creds, err := LoadMiniMaxCredentials()
	if err != nil {
		t.Fatalf("LoadMiniMaxCredentials() error = %v", err)
	}
	if creds.MiniMaxClientID != MiniMaxDefaultClientID {
		t.Fatalf("default MiniMax client ID = %q", creds.MiniMaxClientID)
	}

	t.Setenv("V100_MINIMAX_CLIENT_ID", "env-minimax")
	creds, err = LoadMiniMaxCredentials()
	if err != nil {
		t.Fatalf("LoadMiniMaxCredentials() with env error = %v", err)
	}
	if creds.MiniMaxClientID != "env-minimax" {
		t.Fatalf("MiniMax client ID = %q, want env-minimax", creds.MiniMaxClientID)
	}
}

func TestLoadMiniMaxCredentialsPlaintextFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withSecretManagers(t)
	warnings := capturePlaintextWarnings(t)
	if err := os.MkdirAll(filepath.Dir(DefaultCredentialsPath()), 0o755); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	if err := os.WriteFile(DefaultCredentialsPath(), []byte(`{"minimax_client_id":"file-minimax"}`), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	creds, err := LoadMiniMaxCredentials()
	if err != nil {
		t.Fatalf("LoadMiniMaxCredentials() error = %v", err)
	}
	if creds.MiniMaxClientID != "file-minimax" {
		t.Fatalf("MiniMax client ID = %q", creds.MiniMaxClientID)
	}
	if !strings.Contains(warnings.String(), "plaintext OAuth credentials") {
		t.Fatalf("expected warning, got %q", warnings.String())
	}
}

func TestOAuthCredentialHelpersUnknownFields(t *testing.T) {
	if _, err := resolveOAuthCredentialField(context.Background(), "unknown"); err == nil {
		t.Fatal("resolveOAuthCredentialField() returned nil error for unknown field")
	}
	c := &OAuthCredentials{}
	setCredentialValue(c, "unknown", "value")
	if got := credentialValue(c, "unknown"); got != "" {
		t.Fatalf("unknown credential value = %q", got)
	}
	err := missingOAuthCredentialsError([]string{"unknown"}, "/tmp/fallback.json")
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("missing error = %v", err)
	}
}

func TestOAuthConfigBuilders(t *testing.T) {
	creds := &OAuthCredentials{
		CodexClientID:      "codex-id",
		GeminiClientID:     "gemini-id",
		GeminiClientSecret: "gemini-secret",
	}
	codex := CodexOAuthConfig(creds)
	if codex.ClientID != "codex-id" || codex.ClientSecret != "" || codex.TokenURL != CodexTokenURL {
		t.Fatalf("unexpected codex config: %+v", codex)
	}
	gemini := GeminiOAuthConfig(creds)
	if gemini.ClientID != "gemini-id" || gemini.ClientSecret != "gemini-secret" || gemini.ExtraParams["prompt"] != "consent" {
		t.Fatalf("unexpected gemini config: %+v", gemini)
	}
}

func TestPostTokenRequestResponses(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Fatalf("Content-Type = %q", got)
			}
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
		}))
		defer srv.Close()
		tok, err := postTokenRequest(context.Background(), srv.URL, url.Values{"client_id": {"cid"}})
		if err != nil {
			t.Fatalf("postTokenRequest() error = %v", err)
		}
		if tok.Access != "access" || tok.Refresh != "refresh" || !tok.Valid() {
			t.Fatalf("unexpected token: %+v", tok)
		}
	})

	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{name: "http error", code: http.StatusUnauthorized, body: "denied", want: "HTTP 401"},
		{name: "invalid json", code: http.StatusOK, body: "{", want: "parse token response"},
		{name: "empty token", code: http.StatusOK, body: `{"expires_in":1}`, want: "empty access_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := postTokenRequest(context.Background(), srv.URL, url.Values{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRefreshUsesEnvCredentialsAndTokenEndpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("V100_CODEX_CLIENT_ID", "codex-env")
	oldURL := codexTokenURL
	defer func() { codexTokenURL = oldURL }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := mustFormValue(t, r, "client_id"); got != "codex-env" {
			t.Fatalf("client_id = %q", got)
		}
		if got := mustFormValue(t, r, "refresh_token"); got != "old-refresh" {
			t.Fatalf("refresh_token = %q", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	codexTokenURL = srv.URL

	tok, err := Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if tok.Access != "new-access" || tok.Refresh != "new-refresh" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

func TestRefreshGeminiUsesEnvCredentialsAndTokenEndpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("V100_GEMINI_CLIENT_ID", "gemini-env")
	t.Setenv("V100_GEMINI_CLIENT_SECRET", "gemini-secret")
	oldURL := geminiTokenURL
	defer func() { geminiTokenURL = oldURL }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := mustFormValue(t, r, "client_secret"); got != "gemini-secret" {
			t.Fatalf("client_secret = %q", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"gemini-access","expires_in":3600}`))
	}))
	defer srv.Close()
	geminiTokenURL = srv.URL

	tok, err := RefreshGemini(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshGemini() error = %v", err)
	}
	if tok.Access != "gemini-access" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

func TestLoginWithConfigCompletesCallbackAndTokenExchange(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := mustFormValue(t, r, "grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q", got)
		}
		if got := mustFormValue(t, r, "code"); got != "callback-code" {
			t.Fatalf("code = %q", got)
		}
		if mustFormValue(t, r, "code_verifier") == "" {
			t.Fatal("empty code_verifier")
		}
		_, _ = w.Write([]byte(`{"access_token":"login-access","refresh_token":"login-refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	addr := freeLocalAddr(t)
	oldOpen := openBrowser
	defer func() { openBrowser = oldOpen }()
	openBrowser = func(authLink string) {
		go func() {
			u, err := url.Parse(authLink)
			if err != nil {
				return
			}
			callback := "http://" + addr + "/callback?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=callback-code"
			_, _ = http.Get(callback)
		}()
	}

	tok, err := LoginWithConfig(context.Background(), OAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AuthURL:      "http://auth.local/authorize",
		TokenURL:     tokenServer.URL,
		RedirectURI:  "http://" + addr + "/callback",
		Scopes:       "scope-a",
		ExtraParams:  map[string]string{"access_type": "offline"},
	})
	if err != nil {
		t.Fatalf("LoginWithConfig() error = %v", err)
	}
	if tok.Access != "login-access" || tok.Refresh != "login-refresh" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

func TestMiniMaxLoginAndRefreshUseConfiguredSecrets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("V100_MINIMAX_CLIENT_ID", "minimax-env")
	oldCodeURL, oldTokenURL, oldMinInterval, oldOpen := miniMaxCodeURL, miniMaxTokenURL, miniMaxMinPollInterval, openBrowser
	defer func() {
		miniMaxCodeURL = oldCodeURL
		miniMaxTokenURL = oldTokenURL
		miniMaxMinPollInterval = oldMinInterval
		openBrowser = oldOpen
	}()
	miniMaxMinPollInterval = time.Millisecond
	openBrowser = func(string) {}

	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/code":
			state := mustFormValue(t, r, "state")
			if got := mustFormValue(t, r, "client_id"); got != "minimax-env" {
				t.Fatalf("client_id = %q", got)
			}
			_, _ = fmt.Fprintf(w, `{"user_code":"USER-CODE","verification_uri":"https://example.com/verify","expired_in":%d,"interval":1,"state":%q}`, time.Now().Add(time.Second).Unix(), state)
		case "/token":
			tokenCalls++
			if got := mustFormValue(t, r, "client_id"); got != "minimax-env" {
				t.Fatalf("client_id = %q", got)
			}
			switch mustFormValue(t, r, "grant_type") {
			case MiniMaxGrantType:
				_, _ = w.Write([]byte(`{"status":"success","access_token":"mini-access","refresh_token":"mini-refresh","expired_in":4102444800}`))
			case "refresh_token":
				_, _ = w.Write([]byte(`{"status":"success","access_token":"mini-refreshed","expired_in":4102444800}`))
			default:
				t.Fatalf("unexpected grant type")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	miniMaxCodeURL = srv.URL + "/code"
	miniMaxTokenURL = srv.URL + "/token"

	tok, err := LoginMiniMax(context.Background())
	if err != nil {
		t.Fatalf("LoginMiniMax() error = %v", err)
	}
	if tok.Access != "mini-access" || tok.Refresh != "mini-refresh" {
		t.Fatalf("unexpected minimax token: %+v", tok)
	}

	refreshed, err := RefreshMiniMax(context.Background(), &OAuthCredentials{MiniMaxClientID: "minimax-env"}, "old-refresh")
	if err != nil {
		t.Fatalf("RefreshMiniMax() error = %v", err)
	}
	if refreshed.Access != "mini-refreshed" || refreshed.Refresh != "old-refresh" {
		t.Fatalf("unexpected refreshed token: %+v", refreshed)
	}
	if tokenCalls < 2 {
		t.Fatalf("token endpoint calls = %d, want at least 2", tokenCalls)
	}
}

func TestMiniMaxLoginErrorBranches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("V100_MINIMAX_CLIENT_ID", "minimax-env")
	oldCodeURL, oldTokenURL, oldMinInterval, oldOpen := miniMaxCodeURL, miniMaxTokenURL, miniMaxMinPollInterval, openBrowser
	defer func() {
		miniMaxCodeURL = oldCodeURL
		miniMaxTokenURL = oldTokenURL
		miniMaxMinPollInterval = oldMinInterval
		openBrowser = oldOpen
	}()
	miniMaxMinPollInterval = time.Millisecond
	openBrowser = func(string) {}

	for _, tc := range []struct {
		name       string
		codeBody   func(state string) string
		codeStatus int
		tokenBody  string
		want       string
	}{
		{
			name:       "code http error",
			codeStatus: http.StatusBadGateway,
			codeBody:   func(string) string { return "bad gateway" },
			want:       "code endpoint HTTP 502",
		},
		{
			name:       "code invalid json",
			codeStatus: http.StatusOK,
			codeBody:   func(string) string { return "{" },
			want:       "parse code response",
		},
		{
			name:       "code incomplete",
			codeStatus: http.StatusOK,
			codeBody:   func(state string) string { return fmt.Sprintf(`{"state":%q}`, state) },
			want:       "incomplete code response",
		},
		{
			name:       "state mismatch",
			codeStatus: http.StatusOK,
			codeBody: func(string) string {
				return fmt.Sprintf(`{"user_code":"USER","verification_uri":"https://example.com","expired_in":%d,"interval":1,"state":"wrong"}`, time.Now().Add(time.Second).Unix())
			},
			want: "state mismatch",
		},
		{
			name:       "token error",
			codeStatus: http.StatusOK,
			codeBody: func(state string) string {
				return fmt.Sprintf(`{"user_code":"USER","verification_uri":"https://example.com","expired_in":%d,"interval":1,"state":%q}`, time.Now().Add(time.Second).Unix(), state)
			},
			tokenBody: `{"status":"error","message":"denied"}`,
			want:      "MiniMax OAuth error",
		},
		{
			name:       "token incomplete success",
			codeStatus: http.StatusOK,
			codeBody: func(state string) string {
				return fmt.Sprintf(`{"user_code":"USER","verification_uri":"https://example.com","expired_in":%d,"interval":1,"state":%q}`, time.Now().Add(time.Second).Unix(), state)
			},
			tokenBody: `{"status":"success","access_token":"only-access"}`,
			want:      "incomplete token payload",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/code":
					w.WriteHeader(tc.codeStatus)
					_, _ = w.Write([]byte(tc.codeBody(mustFormValue(t, r, "state"))))
				case "/token":
					_, _ = w.Write([]byte(tc.tokenBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			miniMaxCodeURL = srv.URL + "/code"
			miniMaxTokenURL = srv.URL + "/token"

			_, err := LoginMiniMax(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAccountIDFromJWT(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-123"}}`))
	if got := AccountIDFromJWT("header." + payload + ".sig"); got != "acct-123" {
		t.Fatalf("AccountIDFromJWT() = %q", got)
	}
	for _, token := range []string{"bad", "a.@@@.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".c"} {
		if got := AccountIDFromJWT(token); got != "" {
			t.Fatalf("AccountIDFromJWT(%q) = %q, want empty", token, got)
		}
	}
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error = %v", err)
	}
	if verifier == "" || challenge == "" || strings.Contains(verifier, "=") || strings.Contains(challenge, "=") {
		t.Fatalf("invalid PKCE pair verifier=%q challenge=%q", verifier, challenge)
	}
	if verifier == challenge {
		t.Fatal("verifier and challenge should differ")
	}
}

func mustFormValue(t *testing.T, r *http.Request, key string) string {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	return r.Form.Get(key)
}

func freeLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func withSecretManagers(t *testing.T, managers ...SecretManager) {
	t.Helper()
	old := defaultSecretManagers
	defaultSecretManagers = func() []SecretManager { return managers }
	t.Cleanup(func() { defaultSecretManagers = old })
}

func capturePlaintextWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := plaintextFallbackWarningWriter
	plaintextFallbackWarningWriter = &buf
	t.Cleanup(func() { plaintextFallbackWarningWriter = old })
	return &buf
}

type fakeSecretManager struct {
	name   string
	values map[string]string
	err    error
}

func (m fakeSecretManager) Name() string {
	if m.name == "" {
		return "fake"
	}
	return m.name
}

func (m fakeSecretManager) Get(_ context.Context, key string) (string, error) {
	if value, ok := m.values[key]; ok {
		return value, nil
	}
	if m.err != nil {
		return "", m.err
	}
	return "", ErrSecretUnavailable
}

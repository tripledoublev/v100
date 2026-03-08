package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OAuth endpoint URLs (not secret — these are public API endpoints).
const (
	CodexAuthURL     = "https://auth.openai.com/oauth/authorize"
	CodexTokenURL    = "https://auth.openai.com/oauth/token"
	CodexRedirectURI = "http://localhost:1455/auth/callback"
	CodexScopes      = "openid profile email offline_access"

	GeminiAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	GeminiTokenURL    = "https://oauth2.googleapis.com/token"
	GeminiRedirectURI = "http://127.0.0.1:8085/oauth2callback"
	GeminiScopes      = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"

	MiniMaxCodeURL  = "https://api.minimax.io/oauth/code"
	MiniMaxTokenURL = "https://api.minimax.io/oauth/token"
	MiniMaxScopes   = "group_id profile model.completion"
	MiniMaxGrantType = "urn:ietf:params:oauth:grant-type:user_code"

	// MiniMaxDefaultClientID is the public Client ID for the MiniMax Coding Plan.
	MiniMaxDefaultClientID = "78257093-7e40-4613-99e0-527b14b39113"
)

// OAuthCredentials holds client credentials loaded from disk.
type OAuthCredentials struct {
	CodexClientID      string `json:"codex_client_id"`
	GeminiClientID     string `json:"gemini_client_id"`
	GeminiClientSecret string `json:"gemini_client_secret"`
	MiniMaxClientID    string `json:"minimax_client_id"`
}

// DefaultCredentialsPath returns ~/.config/v100/oauth_credentials.json.
func DefaultCredentialsPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "v100", "oauth_credentials.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "v100", "oauth_credentials.json")
}

// CredentialsTemplate returns a stub oauth_credentials.json payload.
func CredentialsTemplate() string {
	return "{\n  \"codex_client_id\": \"\",\n  \"gemini_client_id\": \"\",\n  \"gemini_client_secret\": \"\",\n  \"minimax_client_id\": \"\"\n}\n"
}

// LoadCodexCredentials reads and validates the Codex OAuth client config.
func LoadCodexCredentials() (*OAuthCredentials, error) {
	return loadCredentials("codex_client_id")
}

// LoadGeminiCredentials reads and validates the Gemini OAuth client config.
func LoadGeminiCredentials() (*OAuthCredentials, error) {
	return loadCredentials("gemini_client_id", "gemini_client_secret")
}

// LoadMiniMaxCredentials reads and validates the MiniMax OAuth client config.
// Falls back to MiniMaxDefaultClientID if missing from config.
func LoadMiniMaxCredentials() (*OAuthCredentials, error) {
	c, err := loadCredentials() // try loading with no required fields
	if err != nil {
		// If file is missing or unreadable, just use the default
		return &OAuthCredentials{MiniMaxClientID: MiniMaxDefaultClientID}, nil
	}
	if strings.TrimSpace(c.MiniMaxClientID) == "" {
		c.MiniMaxClientID = MiniMaxDefaultClientID
	}
	return c, nil
}

func loadCredentials(requiredFields ...string) (*OAuthCredentials, error) {
	path := DefaultCredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if len(requiredFields) == 0 {
			return &OAuthCredentials{}, nil
		}
		return nil, fmt.Errorf("auth: read %s: %w\n  → create it with JSON keys: %s\n  → see: v100 doctor", path, err, strings.Join(requiredFields, ", "))
	}
	var c OAuthCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("auth: parse %s: %w", path, err)
	}
	missing := missingCredentialFields(&c, requiredFields...)
	if len(missing) > 0 {
		return nil, fmt.Errorf("auth: missing %s in %s\n  → fill the required OAuth client values and retry\n  → see: v100 doctor", strings.Join(missing, ", "), path)
	}
	return &c, nil
}

func missingCredentialFields(c *OAuthCredentials, requiredFields ...string) []string {
	var missing []string
	for _, field := range requiredFields {
		if strings.TrimSpace(credentialValue(c, field)) == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

func credentialValue(c *OAuthCredentials, field string) string {
	switch field {
	case "codex_client_id":
		return c.CodexClientID
	case "gemini_client_id":
		return c.GeminiClientID
	case "gemini_client_secret":
		return c.GeminiClientSecret
	case "minimax_client_id":
		return c.MiniMaxClientID
	default:
		return ""
	}
}

// OAuthConfig describes parameters for a PKCE OAuth authorization code flow.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string // empty for public clients (Codex)
	AuthURL      string
	TokenURL     string
	RedirectURI  string
	Scopes       string
	ExtraParams  map[string]string // e.g. access_type=offline
}

// CodexOAuthConfig returns the OAuth config for the Codex (OpenAI) provider.
func CodexOAuthConfig(creds *OAuthCredentials) OAuthConfig {
	return OAuthConfig{
		ClientID:    creds.CodexClientID,
		AuthURL:     CodexAuthURL,
		TokenURL:    CodexTokenURL,
		RedirectURI: CodexRedirectURI,
		Scopes:      CodexScopes,
	}
}

// GeminiOAuthConfig returns the OAuth config for the Gemini provider.
func GeminiOAuthConfig(creds *OAuthCredentials) OAuthConfig {
	return OAuthConfig{
		ClientID:     creds.GeminiClientID,
		ClientSecret: creds.GeminiClientSecret,
		AuthURL:      GeminiAuthURL,
		TokenURL:     GeminiTokenURL,
		RedirectURI:  GeminiRedirectURI,
		Scopes:       GeminiScopes,
		ExtraParams:  map[string]string{"access_type": "offline", "prompt": "consent"},
	}
}

// Login performs a PKCE OAuth authorization code flow for Codex (OpenAI).
func Login(ctx context.Context) (*Token, error) {
	creds, err := LoadCodexCredentials()
	if err != nil {
		return nil, err
	}
	return LoginWithConfig(ctx, CodexOAuthConfig(creds))
}

// LoginGemini performs a PKCE OAuth authorization code flow for Gemini.
func LoginGemini(ctx context.Context) (*Token, error) {
	creds, err := LoadGeminiCredentials()
	if err != nil {
		return nil, err
	}
	return LoginWithConfig(ctx, GeminiOAuthConfig(creds))
}

// LoginWithConfig performs a PKCE OAuth authorization code flow using the given config.
func LoginWithConfig(ctx context.Context, cfg OAuthConfig) (*Token, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("auth: generate state: %w", err)
	}
	state := fmt.Sprintf("%x", stateBytes)

	// Channel to receive the callback code
	type callbackResult struct {
		code string
		err  error
	}
	ch := make(chan callbackResult, 1)

	// Extract callback path from redirect URI
	redirectURL, err := url.Parse(cfg.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("auth: parse redirect URI: %w", err)
	}
	callbackPath := redirectURL.Path
	if callbackPath == "" {
		callbackPath = "/"
	}
	listenAddr := redirectURL.Host

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			ch <- callbackResult{err: fmt.Errorf("auth: state mismatch")}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			ch <- callbackResult{err: fmt.Errorf("auth: no code in callback")}
			return
		}
		_, _ = fmt.Fprintln(w, "<html><body><h2>Authentication successful — you may close this tab.</h2></body></html>")
		ch <- callbackResult{code: code}
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("auth: listen %s: %w", listenAddr, err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)                         //nolint:errcheck
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	// Build authorization URL
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {cfg.RedirectURI},
		"scope":                 {cfg.Scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	for k, v := range cfg.ExtraParams {
		params.Set(k, v)
	}
	authLink := cfg.AuthURL + "?" + params.Encode()

	fmt.Printf("Opening browser for authentication...\n%s\n\n", authLink)
	openBrowser(authLink)

	// Wait for callback (with context timeout)
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return exchangeCodeWithConfig(ctx, cfg, res.code, verifier)
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: login cancelled: %w", ctx.Err())
	}
}

// Refresh exchanges a refresh token for a new access token (Codex/OpenAI).
func Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	creds, err := LoadCodexCredentials()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {creds.CodexClientID},
	}
	return postTokenRequest(ctx, CodexTokenURL, form)
}

// RefreshGemini exchanges a refresh token for a new access token (Gemini).
func RefreshGemini(ctx context.Context, refreshToken string) (*Token, error) {
	creds, err := LoadGeminiCredentials()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {creds.GeminiClientID},
		"client_secret": {creds.GeminiClientSecret},
	}
	return postTokenRequest(ctx, GeminiTokenURL, form)
}

// AccountIDFromJWT decodes the JWT payload and extracts chatgpt_account_id.
func AccountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try with padding
		s := parts[1]
		switch len(s) % 4 {
		case 2:
			s += "=="
		case 3:
			s += "="
		}
		s = strings.ReplaceAll(s, "-", "+")
		s = strings.ReplaceAll(s, "_", "/")
		decoded, err = base64.StdEncoding.DecodeString(s)
		if err != nil {
			return ""
		}
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	authRaw, ok := claims["https://api.openai.com/auth"]
	if !ok {
		return ""
	}
	var auth struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		return ""
	}
	return auth.AccountID
}

// exchangeCodeWithConfig POSTs authorization_code + verifier to the token endpoint.
func exchangeCodeWithConfig(ctx context.Context, cfg OAuthConfig, code, verifier string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.ClientID},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"code_verifier": {verifier},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	return postTokenRequest(ctx, cfg.TokenURL, form)
}

// postTokenRequest is the shared token endpoint caller.
func postTokenRequest(ctx context.Context, tokenURL string, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: token endpoint HTTP %d: %s", resp.StatusCode, raw)
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("auth: parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("auth: empty access_token in response")
	}

	t := &Token{
		Access:    tok.AccessToken,
		Refresh:   tok.RefreshToken,
		ExpiresMS: time.Now().UnixMilli() + int64(tok.ExpiresIn)*1000,
		AccountID: AccountIDFromJWT(tok.AccessToken),
	}
	return t, nil
}

// LoginMiniMax performs an OAuth Device Flow for MiniMax.
// Protocol matches the OpenClaw minimax-portal-auth extension:
// form-urlencoded requests, state parameter, PKCE, user_code polling.
func LoginMiniMax(ctx context.Context) (*MiniMaxToken, error) {
	creds, err := LoadMiniMaxCredentials()
	if err != nil {
		return nil, err
	}

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("auth: generate state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Step 1: Request a device code (form-urlencoded, like OpenClaw)
	codeForm := url.Values{
		"response_type":         {"code"},
		"client_id":             {creds.MiniMaxClientID},
		"scope":                 {MiniMaxScopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	codeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, MiniMaxCodeURL, strings.NewReader(codeForm.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build code request: %w", err)
	}
	codeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	codeReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	codeResp, err := client.Do(codeReq)
	if err != nil {
		return nil, fmt.Errorf("auth: code request: %w", err)
	}
	codeRaw, _ := io.ReadAll(codeResp.Body)
	_ = codeResp.Body.Close()

	if codeResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: code endpoint HTTP %d: %s", codeResp.StatusCode, codeRaw)
	}

	var codeResult struct {
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiredIn       int64  `json:"expired_in"` // unix timestamp, not duration
		Interval        int    `json:"interval"`
		State           string `json:"state"`
	}
	if err := json.Unmarshal(codeRaw, &codeResult); err != nil {
		return nil, fmt.Errorf("auth: parse code response: %w", err)
	}
	if codeResult.UserCode == "" || codeResult.VerificationURI == "" {
		return nil, fmt.Errorf("auth: incomplete code response: %s", codeRaw)
	}
	if codeResult.State != state {
		return nil, fmt.Errorf("auth: state mismatch in code response")
	}

	fmt.Printf("\nVisit: %s\nEnter code: %s\n\n", codeResult.VerificationURI, codeResult.UserCode)
	openBrowser(codeResult.VerificationURI)

	// Step 2: Poll for token (form-urlencoded)
	interval := time.Duration(codeResult.Interval) * time.Millisecond
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	// expired_in is a unix timestamp (seconds)
	deadline := time.Unix(codeResult.ExpiredIn, 0)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("auth: login cancelled: %w", ctx.Err())
		case <-time.After(interval):
		}

		tokenForm := url.Values{
			"grant_type":    {MiniMaxGrantType},
			"client_id":     {creds.MiniMaxClientID},
			"user_code":     {codeResult.UserCode},
			"code_verifier": {verifier},
		}
		tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, MiniMaxTokenURL, strings.NewReader(tokenForm.Encode()))
		if err != nil {
			return nil, fmt.Errorf("auth: build token request: %w", err)
		}
		tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tokenReq.Header.Set("Accept", "application/json")

		tokenResp, err := client.Do(tokenReq)
		if err != nil {
			return nil, fmt.Errorf("auth: token request: %w", err)
		}
		tokenRaw, _ := io.ReadAll(tokenResp.Body)
		_ = tokenResp.Body.Close()

		var tok struct {
			Status      string `json:"status"`
			AccessToken string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiredIn   int64  `json:"expired_in"` // unix timestamp
		}
		if err := json.Unmarshal(tokenRaw, &tok); err != nil {
			continue
		}

		switch tok.Status {
		case "success":
			if tok.AccessToken == "" || tok.RefreshToken == "" {
				return nil, fmt.Errorf("auth: incomplete token payload: %s", tokenRaw)
			}
			return &MiniMaxToken{
				Access:    tok.AccessToken,
				Refresh:   tok.RefreshToken,
				ExpiresMS: tok.ExpiredIn * 1000, // convert unix seconds → ms
			}, nil
		case "error":
			return nil, fmt.Errorf("auth: MiniMax OAuth error: %s", tokenRaw)
		default:
			// "pending" or any other status — keep polling
			continue
		}
	}

	return nil, fmt.Errorf("auth: device code expired — try again")
}

// RefreshMiniMax exchanges a refresh token for a new MiniMax access token.
func RefreshMiniMax(ctx context.Context, creds *OAuthCredentials, refreshToken string) (*MiniMaxToken, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {creds.MiniMaxClientID},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, MiniMaxTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: refresh endpoint HTTP %d: %s", resp.StatusCode, raw)
	}

	var tok struct {
		Status       string `json:"status"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiredIn    int64  `json:"expired_in"` // unix timestamp
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("auth: parse refresh response: %w", err)
	}
	if tok.Status != "success" || tok.AccessToken == "" {
		return nil, fmt.Errorf("auth: refresh failed: %s", raw)
	}

	t := &MiniMaxToken{
		Access:    tok.AccessToken,
		Refresh:   tok.RefreshToken,
		ExpiresMS: tok.ExpiredIn * 1000,
	}
	if t.Refresh == "" {
		t.Refresh = refreshToken // keep old refresh token if not rotated
	}
	return t, nil
}

// openBrowser attempts to open the URL in the default browser.
func openBrowser(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	_ = exec.Command(cmd, args...).Start()
}

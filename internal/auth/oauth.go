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
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	ClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthURL     = "https://auth.openai.com/oauth/authorize"
	TokenURL    = "https://auth.openai.com/oauth/token"
	RedirectURI = "http://localhost:1455/auth/callback"
	Scope       = "openid profile email offline_access"
)

// Login performs a PKCE OAuth authorization code flow.
// It opens the user's browser, waits for the callback on localhost:1455,
// exchanges the code for tokens, and returns the resulting Token.
func Login(ctx context.Context) (*Token, error) {
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

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
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
		fmt.Fprintln(w, "<html><body><h2>Authentication successful — you may close this tab.</h2></body></html>")
		ch <- callbackResult{code: code}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, fmt.Errorf("auth: listen :1455: %w", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	// Build authorization URL
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {ClientID},
		"redirect_uri":          {RedirectURI},
		"scope":                 {Scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authLink := AuthURL + "?" + params.Encode()

	fmt.Printf("Opening browser for authentication...\n%s\n\n", authLink)
	openBrowser(authLink)

	// Wait for callback (with context timeout)
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return exchangeCode(ctx, res.code, verifier)
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: login cancelled: %w", ctx.Err())
	}
}

// Refresh exchanges a refresh token for a new access token.
func Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	}
	return postTokenRequest(ctx, form)
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

// exchangeCode POSTs authorization_code + verifier to TokenURL.
func exchangeCode(ctx context.Context, code, verifier string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"code_verifier": {verifier},
	}
	return postTokenRequest(ctx, form)
}

// postTokenRequest is the shared token endpoint caller.
func postTokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: token request: %w", err)
	}
	defer resp.Body.Close()
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

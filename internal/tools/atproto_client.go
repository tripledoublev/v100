package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

// atProtoClient handles authentication and XRPC requests to a Bluesky PDS.
type atProtoClient struct {
	cfg     config.ATProtoConfig
	httpCli *http.Client
	baseURL string
	session atProtoSession
}

type atProtoSession struct {
	AccessJwt string `json:"accessJwt"`
	DID       string `json:"did"`
	Handle    string `json:"handle"`
}

func newATProtoClient(cfg config.ATProtoConfig) *atProtoClient {
	pds := cfg.PDSURL
	if pds == "" {
		pds = "https://bsky.social"
	}
	pds = strings.TrimRight(pds, "/")
	return &atProtoClient{
		cfg:     cfg,
		httpCli: &http.Client{Timeout: 20 * time.Second},
		baseURL: pds,
	}
}

// appPassword resolves the app password from config or env.
func (c *atProtoClient) appPassword() (string, error) {
	if c.cfg.AppPassword != "" {
		return c.cfg.AppPassword, nil
	}
	if c.cfg.AppPasswordEnv != "" {
		val := os.Getenv(c.cfg.AppPasswordEnv)
		if val != "" {
			return val, nil
		}
		return "", fmt.Errorf("atproto: env var %s is not set", c.cfg.AppPasswordEnv)
	}
	return "", fmt.Errorf("atproto: no app_password or app_password_env configured")
}

// login authenticates with the PDS and caches the session.
func (c *atProtoClient) login() error {
	if c.cfg.Handle == "" {
		return fmt.Errorf("atproto: handle is not configured")
	}
	pw, err := c.appPassword()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{
		"identifier": c.cfg.Handle,
		"password":   pw,
	})
	resp, err := c.httpCli.Post(
		c.baseURL+"/xrpc/com.atproto.server.createSession",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("atproto: login request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("atproto: login failed (%d): %s", resp.StatusCode, string(data))
	}
	if err := json.Unmarshal(data, &c.session); err != nil {
		return fmt.Errorf("atproto: parse session: %w", err)
	}
	return nil
}

// xrpcGet performs a GET XRPC query.
func (c *atProtoClient) xrpcGet(nsid string, params url.Values) ([]byte, error) {
	u := c.baseURL + "/xrpc/" + nsid
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.session.AccessJwt != "" {
		req.Header.Set("Authorization", "Bearer "+c.session.AccessJwt)
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("atproto: GET %s: %w", nsid, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("atproto: GET %s (%d): %s", nsid, resp.StatusCode, string(data))
	}
	return data, nil
}

// xrpcPost performs a POST XRPC procedure.
func (c *atProtoClient) xrpcPost(nsid string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/xrpc/"+nsid, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.session.AccessJwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("atproto: POST %s: %w", nsid, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("atproto: POST %s (%d): %s", nsid, resp.StatusCode, string(data))
	}
	return data, nil
}

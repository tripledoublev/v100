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

// pickATProtoAccount returns the ATProtoConfig for "", "main", or "alt".
func pickATProtoAccount(cfg *config.Config, account string) (config.ATProtoConfig, error) {
	switch strings.TrimSpace(account) {
	case "", "main":
		return cfg.ATProto, nil
	case "alt":
		return cfg.ATProtoAlt, nil
	default:
		return config.ATProtoConfig{}, fmt.Errorf("atproto: unknown account %q (want \"main\" or \"alt\")", account)
	}
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

// BlobInfo is the subset of the uploadBlob response needed to embed a blob in
// a record (e.g. app.bsky.embed.images).
type BlobInfo struct {
	CID  string
	Mime string
	Size int64
}

// xrpcUploadBlob uploads a file to the PDS via com.atproto.repo.uploadBlob and
// returns the resulting blob's CID, MIME type, and size.
func (c *atProtoClient) xrpcUploadBlob(filename, mimeType string, data []byte) (BlobInfo, error) {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/xrpc/com.atproto.repo.uploadBlob", bytes.NewReader(data))
	if err != nil {
		return BlobInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.session.AccessJwt)
	req.Header.Set("Content-Type", mimeType)
	req.ContentLength = int64(len(data))

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return BlobInfo{}, fmt.Errorf("atproto: upload blob: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return BlobInfo{}, fmt.Errorf("atproto: uploadBlob status %d: %s", resp.StatusCode, string(respData))
	}

	var out struct {
		Blob struct {
			Ref struct {
				Link string `json:"$link"`
			} `json:"ref"`
			MimeType string `json:"mimeType"`
			Size     int64  `json:"size"`
		} `json:"blob"`
	}
	if err := json.Unmarshal(respData, &out); err != nil {
		return BlobInfo{}, fmt.Errorf("atproto: decode uploadBlob response: %w", err)
	}
	return BlobInfo{CID: out.Blob.Ref.Link, Mime: out.Blob.MimeType, Size: out.Blob.Size}, nil
}

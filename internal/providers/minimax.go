package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/auth"
)

const (
	minimaxBaseURL      = "https://api.minimax.io/anthropic/v1/messages"
	minimaxDefaultModel = "MiniMax-M2.5"
)

// MiniMaxProvider implements Provider using MiniMax's Anthropic-compatible
// Messages API, authenticated via an OAuth subscription token.
type MiniMaxProvider struct {
	mu           sync.Mutex
	token        auth.MiniMaxToken
	tokenPath    string
	defaultModel string
	client       *http.Client
}

// NewMiniMaxProvider creates a provider that loads its OAuth token from tokenPath.
func NewMiniMaxProvider(tokenPath, defaultModel string) (*MiniMaxProvider, error) {
	if tokenPath == "" {
		tokenPath = auth.DefaultMiniMaxTokenPath()
	}
	if defaultModel == "" {
		defaultModel = minimaxDefaultModel
	}
	p := &MiniMaxProvider{
		tokenPath:    tokenPath,
		defaultModel: defaultModel,
		client:       &http.Client{Timeout: 180 * time.Second},
	}
	t, err := auth.LoadMiniMax(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("minimax: %w\n  → run 'v100 login --provider minimax' to authenticate", err)
	}
	p.token = *t
	return p, nil
}

func (p *MiniMaxProvider) Name() string { return "minimax" }

func (p *MiniMaxProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true}
}

// accessToken returns a valid access token, refreshing if expired.
func (p *MiniMaxProvider) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.token.Valid() {
		creds, err := auth.LoadMiniMaxCredentials()
		if err != nil {
			return "", fmt.Errorf("minimax: load credentials for refresh: %w", err)
		}
		refreshed, err := auth.RefreshMiniMax(ctx, creds, p.token.Refresh)
		if err != nil {
			return "", fmt.Errorf("minimax: token refresh failed: %w\n  → run 'v100 login --provider minimax' to re-authenticate", err)
		}
		p.token.Access = refreshed.Access
		p.token.ExpiresMS = refreshed.ExpiresMS
		if refreshed.Refresh != "" {
			p.token.Refresh = refreshed.Refresh
		}
		if saveErr := auth.SaveMiniMax(p.tokenPath, &p.token); saveErr != nil {
			fmt.Printf("minimax: warning: could not save refreshed token: %v\n", saveErr)
		}
	}
	return p.token.Access, nil
}

func (p *MiniMaxProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	access, err := p.accessToken(ctx)
	if err != nil {
		return CompleteResponse{}, err
	}

	aReq := anthropicBuildRequest(model, req)

	body, err := json.Marshal(aReq)
	if err != nil {
		return CompleteResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, minimaxBaseURL, bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("minimax: request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("read body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		baseErr := fmt.Errorf("minimax: HTTP %d: %s", httpResp.StatusCode, raw)
		var apiErr anthropicError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Message != "" {
			baseErr = fmt.Errorf("minimax: %s: %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		if httpResp.StatusCode == 429 || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			return CompleteResponse{}, &RetryableError{
				Err:        baseErr,
				StatusCode: httpResp.StatusCode,
				RetryAfter: retryAfterFromHeader(httpResp.Header.Get("Retry-After")),
			}
		}
		if strings.Contains(string(raw), "2013") {
			if strings.Contains(string(raw), "context window") || strings.Contains(string(raw), "exceeds limit") {
				return CompleteResponse{}, fmt.Errorf("minimax: context window exceeded — reduce tool result size or use compression")
			}
			return CompleteResponse{}, fmt.Errorf("minimax error 2013: tool results not contiguous with tool calls (message ordering bug): %w", baseErr)
		}
		return CompleteResponse{}, baseErr
	}

	// Subscription-backed — $0 cost
	costFn := func(_, _ int) float64 { return 0 }
	return anthropicParseResponse(raw, costFn)
}

func (p *MiniMaxProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	access, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	aReq := anthropicBuildRequest(model, req)
	aReq.Stream = true

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, minimaxBaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("minimax: request: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		raw, err := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read error body: %w", err)
		}
		baseErr := fmt.Errorf("minimax: HTTP %d: %s", httpResp.StatusCode, raw)
		if httpResp.StatusCode == 429 || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			return nil, &RetryableError{
				Err:        baseErr,
				StatusCode: httpResp.StatusCode,
				RetryAfter: retryAfterFromHeader(httpResp.Header.Get("Retry-After")),
			}
		}
		if strings.Contains(string(raw), "2013") {
			if strings.Contains(string(raw), "context window") || strings.Contains(string(raw), "exceeds limit") {
				return nil, fmt.Errorf("minimax: context window exceeded — reduce tool result size or use compression")
			}
			return nil, fmt.Errorf("minimax error 2013: tool results not contiguous with tool calls (message ordering bug): %w", baseErr)
		}
		return nil, baseErr
	}

	ch := make(chan StreamEvent, 100)
	go minimaxStreamSSE(httpResp, ch)

	return ch, nil
}

// minimaxStreamSSE reads SSE events from a MiniMax streaming response.
// MiniMax uses the Anthropic SSE format; cost is $0 (subscription-backed).
func minimaxStreamSSE(httpResp *http.Response, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() { _ = httpResp.Body.Close() }()

	scanner := bufio.NewScanner(httpResp.Body)
	var currentToolID string
	var trackedUsage Usage // accumulate usage across SSE events

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type         string                 `json:"type"`
			Message      *anthropicResponse     `json:"message"`
			ContentBlock *anthropicContentBlock `json:"content_block"`
			Delta        *struct {
				Type string `json:"type"`
				Text string `json:"text"`
				JSON string `json:"partial_json"`
			} `json:"delta"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			if event.Message != nil {
				trackedUsage.InputTokens = event.Message.Usage.InputTokens
			}
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
				currentToolID = event.ContentBlock.ID
				ch <- StreamEvent{
					Type:         StreamToolCallStart,
					ToolCallID:   currentToolID,
					ToolCallName: event.ContentBlock.Name,
				}
			}
		case "content_block_delta":
			if event.Delta != nil {
				switch event.Delta.Type {
				case "text_delta":
					ch <- StreamEvent{Type: StreamToken, Text: event.Delta.Text}
				case "input_json_delta":
					ch <- StreamEvent{
						Type:         StreamToolCallDelta,
						ToolCallID:   currentToolID,
						ToolCallArgs: event.Delta.JSON,
					}
				}
			}
		case "message_delta":
			if event.Usage != nil {
				trackedUsage.OutputTokens = event.Usage.OutputTokens
			}
		case "message_stop":
			// subscription-backed: $0 cost
			ch <- StreamEvent{Type: StreamDone, Usage: trackedUsage}
			return
		case "error":
			ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("minimax stream error: %s", data)}
			return
		}
	}
}

func (p *MiniMaxProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, fmt.Errorf("minimax: embeddings not supported")
}

func (p *MiniMaxProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}
	return ModelMetadata{
		Model:       model,
		ContextSize: 200000,
		IsFree:      true, // subscription-backed
	}, nil
}

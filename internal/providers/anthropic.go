package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	anthropicBaseURL      = "https://api.anthropic.com/v1/messages"
	anthropicVersion      = "2023-06-01"
	anthropicDefaultModel = "claude-sonnet-4-20250514"
)

// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey       string
	defaultModel string
	client       *http.Client
}

// NewAnthropicProvider creates a provider. It checks for a stored API key at
// storedKeyPath first (empty string uses the default path), then falls back to
// the environment variable authEnv.
func NewAnthropicProvider(authEnv, defaultModel string) (*AnthropicProvider, error) {
	if authEnv == "" {
		authEnv = "ANTHROPIC_API_KEY"
	}
	if defaultModel == "" {
		defaultModel = anthropicDefaultModel
	}

	// Try stored key first, then env var.
	apiKey := resolveAnthropicKey(authEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: no API key found — set %s or run 'v100 login --provider anthropic'", authEnv)
	}

	return &AnthropicProvider{
		apiKey:       apiKey,
		defaultModel: defaultModel,
		client:       &http.Client{Timeout: 180 * time.Second},
	}, nil
}

// resolveAnthropicKey returns the API key from the stored auth file or env var.
func resolveAnthropicKey(authEnv string) string {
	// 1. Check stored auth file.
	path := defaultClaudeTokenPath()
	if data, err := os.ReadFile(path); err == nil {
		var stored struct {
			APIKey string `json:"api_key"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.APIKey != "" {
			return stored.APIKey
		}
	}
	// 2. Fall back to env var.
	return os.Getenv(authEnv)
}

func defaultClaudeTokenPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return xdg + "/v100/anthropic_auth.json"
	}
	home, _ := os.UserHomeDir()
	return home + "/.config/v100/anthropic_auth.json"
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true, Images: true}
}

func (p *AnthropicProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	aReq := anthropicBuildRequest(model, req)

	body, err := json.Marshal(aReq)
	if err != nil {
		return CompleteResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL, bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("anthropic: request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("read body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		baseErr := fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, raw)
		var apiErr anthropicError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Message != "" {
			baseErr = fmt.Errorf("anthropic: %s: %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		if httpResp.StatusCode == 429 || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			return CompleteResponse{}, &RetryableError{
				Err:        baseErr,
				StatusCode: httpResp.StatusCode,
				RetryAfter: retryAfterFromHeader(httpResp.Header.Get("Retry-After")),
			}
		}
		return CompleteResponse{}, baseErr
	}

	costFn := func(input, output int) float64 {
		return anthropicEstimateCost(model, input, output)
	}
	return anthropicParseResponse(raw, costFn)
}

func (p *AnthropicProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	// Anthropic does not currently provide a native embeddings API.
	return EmbedResponse{}, fmt.Errorf("anthropic: embeddings not supported")
}

func (p *AnthropicProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}

	m := ModelMetadata{Model: model, ContextSize: 200000}

	switch {
	case strings.Contains(model, "opus"):
		m.CostPer1MIn = 15.00
		m.CostPer1MOut = 75.00
	case strings.Contains(model, "sonnet-3-5"), strings.Contains(model, "sonnet-3.5"):
		m.CostPer1MIn = 3.00
		m.CostPer1MOut = 15.00
	case strings.Contains(model, "haiku"):
		m.CostPer1MIn = 0.80
		m.CostPer1MOut = 4.00
	}

	return m, nil
}

func (p *AnthropicProvider) Models() []ModelInfo {
	return []ModelInfo{
		{Name: "claude-opus-4-6", Description: "powerful — flagship for agents + coding"},
		{Name: "claude-sonnet-4-6", Description: "standard — speed/intelligence balance"},
		{Name: "claude-haiku-4-5-20251001", Description: "fast — lowest latency"},
	}
}

func (p *AnthropicProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	aReq := anthropicBuildRequest(model, req)
	aReq.Stream = true

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		raw, err := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read error body: %w", err)
		}
		baseErr := fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, raw)
		if httpResp.StatusCode == 429 || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			return nil, &RetryableError{
				Err:        baseErr,
				StatusCode: httpResp.StatusCode,
				RetryAfter: retryAfterFromHeader(httpResp.Header.Get("Retry-After")),
			}
		}
		return nil, baseErr
	}

	ch := make(chan StreamEvent, 100)
	go anthropicStreamSSE(httpResp, model, ch)

	return ch, nil
}

// anthropicStreamSSE reads SSE events from an Anthropic streaming response and
// sends parsed StreamEvents on ch. It closes ch and httpResp.Body when done.
func anthropicStreamSSE(httpResp *http.Response, model string, ch chan<- StreamEvent) {
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
			trackedUsage.CostUSD = anthropicEstimateCost(model, trackedUsage.InputTokens, trackedUsage.OutputTokens)
			ch <- StreamEvent{Type: StreamDone, Usage: trackedUsage}
			return
		case "error":
			ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("anthropic stream error: %s", data)}
			return
		}
	}
}

func anthropicEstimateCost(model string, input, output int) float64 {
	var inPrice, outPrice float64
	switch {
	case strings.Contains(model, "opus"):
		inPrice, outPrice = 15.00, 75.00
	case strings.Contains(model, "sonnet-3-5"), strings.Contains(model, "sonnet-3.5"):
		inPrice, outPrice = 3.00, 15.00
	case strings.Contains(model, "haiku"):
		inPrice, outPrice = 0.80, 4.00
	default: // sonnet and unknown
		inPrice, outPrice = 3.00, 15.00
	}
	return (float64(input)/1_000_000)*inPrice + (float64(output)/1_000_000)*outPrice
}

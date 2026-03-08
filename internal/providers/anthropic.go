package providers

import (
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
	anthropicBaseURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion     = "2023-06-01"
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
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: false}
}

// Anthropic request/response types

type anthropicRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens"`
	System        string               `json:"system,omitempty"`
	Messages      []anthropicMessage   `json:"messages"`
	Tools         []anthropicToolDef   `json:"tools,omitempty"`
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	TopK          *int                 `json:"top_k,omitempty"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
}

type anthropicError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *AnthropicProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	system, messages := anthropicConvertMessages(req.Messages)

	var tools []anthropicToolDef
	for _, ts := range req.Tools {
		tools = append(tools, anthropicToolDef{
			Name:        ts.Name,
			Description: ts.Description,
			InputSchema: ts.InputSchema,
		})
	}

	maxTokens := req.GenParams.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	aReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  messages,
	}
	if len(tools) > 0 {
		aReq.Tools = tools
	}
	if req.GenParams.Temperature != nil {
		aReq.Temperature = req.GenParams.Temperature
	}
	if req.GenParams.TopP != nil {
		aReq.TopP = req.GenParams.TopP
	}
	if req.GenParams.TopK != nil {
		aReq.TopK = req.GenParams.TopK
	}
	if len(req.GenParams.StopSequences) > 0 {
		aReq.StopSequences = req.GenParams.StopSequences
	}

	body, err := json.Marshal(aReq)
	if err != nil {
		return CompleteResponse{}, err
	}

	// Retry loop for 429/5xx (up to 3 attempts)
	for attempt := range 3 {
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

		raw, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if httpResp.StatusCode == http.StatusOK {
			return anthropicParseResponse(raw, model)
		}

		retryable := httpResp.StatusCode == http.StatusTooManyRequests ||
			(httpResp.StatusCode >= 500 && httpResp.StatusCode < 600)
		if retryable && attempt < 2 {
			wait := time.Duration(attempt+1) * 2 * time.Second
			fmt.Printf("anthropic: HTTP %d, retrying in %v…\n", httpResp.StatusCode, wait)
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return CompleteResponse{}, ctx.Err()
			}
		}

		// Try to parse error for a better message
		var apiErr anthropicError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Message != "" {
			return CompleteResponse{}, fmt.Errorf("anthropic: %s: %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		return CompleteResponse{}, fmt.Errorf("anthropic: HTTP %d: %s", httpResp.StatusCode, raw)
	}

	return CompleteResponse{}, fmt.Errorf("anthropic: exhausted retries")
}

func anthropicParseResponse(raw []byte, model string) (CompleteResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return CompleteResponse{}, fmt.Errorf("anthropic: decode: %w", err)
	}

	var text strings.Builder
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			input := block.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: input,
			})
		}
	}

	costUSD := anthropicEstimateCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens)

	return CompleteResponse{
		AssistantText: text.String(),
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CostUSD:      costUSD,
		},
		Raw: raw,
	}, nil
}

// anthropicConvertMessages converts provider messages to Anthropic format.
// Returns (system, messages). System messages are extracted; tool results
// are wrapped in tool_result content blocks within user turns.
func anthropicConvertMessages(msgs []Message) (string, []anthropicMessage) {
	var system string
	var out []anthropicMessage

	// Collect pending tool results to merge into a single user turn
	var pendingToolResults []anthropicContentBlock

	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		blocks := make([]anthropicContentBlock, len(pendingToolResults))
		copy(blocks, pendingToolResults)
		out = append(out, anthropicMessage{
			Role:    "user",
			Content: blocks,
		})
		pendingToolResults = nil
	}

	for _, m := range msgs {
		switch m.Role {
		case "system":
			system = m.Content

		case "user":
			flushToolResults()
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: m.Content,
			})

		case "assistant":
			flushToolResults()
			if len(m.ToolCalls) == 0 {
				out = append(out, anthropicMessage{
					Role:    "assistant",
					Content: m.Content,
				})
			} else {
				var blocks []anthropicContentBlock
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{
						Type: "text",
						Text: m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Args,
					})
				}
				out = append(out, anthropicMessage{
					Role:    "assistant",
					Content: blocks,
				})
			}

		case "tool":
			content := m.Content
			if content == "" {
				content = "(no output)"
			}
			pendingToolResults = append(pendingToolResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   content,
			})
		}
	}
	flushToolResults()

	return system, out
}

func anthropicEstimateCost(model string, input, output int) float64 {
	var inPrice, outPrice float64
	switch {
	case strings.Contains(model, "opus"):
		inPrice, outPrice = 15.00, 75.00
	case strings.Contains(model, "haiku"):
		inPrice, outPrice = 0.80, 4.00
	default: // sonnet and unknown
		inPrice, outPrice = 3.00, 15.00
	}
	return (float64(input)/1_000_000)*inPrice + (float64(output)/1_000_000)*outPrice
}

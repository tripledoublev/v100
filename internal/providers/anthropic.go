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
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true}
}

// Anthropic request/response types

type anthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Tools         []anthropicToolDef `json:"tools,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
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
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(httpResp.Body)
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

	var resp anthropicResponse
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return CompleteResponse{}, err
	}
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

func anthropicParseResponse(model string, raw []byte) (CompleteResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return CompleteResponse{}, err
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
	return CompleteResponse{
		AssistantText: text.String(),
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CostUSD:      anthropicEstimateCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens),
		},
		Raw: raw,
	}, nil
}

func (p *AnthropicProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	// Anthropic does not currently provide a native embeddings API.
	return EmbedResponse{}, fmt.Errorf("anthropic: embeddings not supported")
}

func (p *AnthropicProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
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
		Stream:    true,
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
		raw, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
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
	go func() {
		defer close(ch)
		defer httpResp.Body.Close()

		scanner := bufio.NewScanner(httpResp.Body)
		var currentToolID string

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
				// message_start contains initial usage or metadata if needed
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
					if event.Delta.Type == "text_delta" {
						ch <- StreamEvent{Type: StreamToken, Text: event.Delta.Text}
					} else if event.Delta.Type == "input_json_delta" {
						ch <- StreamEvent{
							Type:         StreamToolCallDelta,
							ToolCallID:   currentToolID,
							ToolCallArgs: event.Delta.JSON,
						}
					}
				}
			case "message_delta":
				// partial usage updates
			case "message_stop":
				// message_stop usually has the final usage block
				u := Usage{}
				if event.Message != nil {
					u = Usage{
						InputTokens:  event.Message.Usage.InputTokens,
						OutputTokens: event.Message.Usage.OutputTokens,
						CostUSD:      anthropicEstimateCost(model, event.Message.Usage.InputTokens, event.Message.Usage.OutputTokens),
					}
				}
				ch <- StreamEvent{Type: StreamDone, Usage: u}
				return
			case "error":
				ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("anthropic stream error: %s", data)}
				return
			}
		}
	}()

	return ch, nil
}

// anthropicConvertMessages converts provider messages to Anthropic format.
// Returns (system, messages). System messages are extracted; tool results
// are wrapped in tool_result content blocks within user turns.
func anthropicConvertMessages(msgs []Message) (string, []anthropicMessage) {
	var system string
	var out []anthropicMessage

	// Collect pending tool results to merge into a single user turn
	var pendingResults []anthropicContentBlock

	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case "system":
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case "user":
			out = append(out, anthropicMessage{Role: "user", Content: m.Content})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var content []anthropicContentBlock
				if m.Content != "" {
					content = append(content, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					content = append(content, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Args,
					})
				}
				out = append(out, anthropicMessage{Role: "assistant", Content: content})
			} else {
				out = append(out, anthropicMessage{Role: "assistant", Content: m.Content})
			}
		case "tool":
			pendingResults = append(pendingResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})

			// If next message is not a tool result, flush pending results into a user turn
			if i+1 == len(msgs) || msgs[i+1].Role != "tool" {
				out = append(out, anthropicMessage{Role: "user", Content: pendingResults})
				pendingResults = nil
			}
		}
	}
	return system, out
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

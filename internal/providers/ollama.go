package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ollamaDefaultBaseURL = "http://localhost:11434"
	ollamaDefaultModel   = "qwen3.5:2b"
)

// OllamaProvider implements Provider using a local or remote Ollama server.
type OllamaProvider struct {
	client       *http.Client
	baseURL      string
	defaultModel string
	username     string
	password     string
}

func NewOllamaProvider(baseURL, defaultModel, username, password string) (*OllamaProvider, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = ollamaDefaultBaseURL
	}
	if strings.TrimSpace(defaultModel) == "" {
		defaultModel = ollamaDefaultModel
	}
	return &OllamaProvider{
		client:       &http.Client{Timeout: 180 * time.Second},
		baseURL:      strings.TrimRight(baseURL, "/"),
		defaultModel: defaultModel,
		username:     username,
		password:     password,
	}, nil
}

func (p *OllamaProvider) Name() string { return "ollama" }

func (p *OllamaProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true}
}

func (p *OllamaProvider) setAuth(req *http.Request) {
	if p.username != "" || p.password != "" {
		req.SetBasicAuth(p.username, p.password)
	}
}

func (p *OllamaProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.defaultModel
	}

	messages := make([]ollamaChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := ollamaChatMessage{Role: m.Role, Content: m.Content, Name: m.Name}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]ollamaToolCallOut, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ollamaToolCallOut{
					Function: ollamaToolCallFunctionOut{Name: tc.Name, Arguments: tc.Args},
				})
			}
		}
		messages = append(messages, msg)
	}

	tools := make([]ollamaToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, ollamaToolDef{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	oReq := ollamaChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	body, err := json.Marshal(oReq)
	if err != nil {
		return nil, err
	}

	url := p.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuth(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		baseErr := fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, string(raw))
		if isRetryableStatus(resp.StatusCode) {
			return nil, &RetryableError{
				Err:        baseErr,
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfterFromHeader(resp.Header.Get("Retry-After")),
			}
		}
		return nil, baseErr
	}

	ch := make(chan StreamEvent, 100)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		dec := json.NewDecoder(resp.Body)
		var toolCallIdx int
		for {
			var chunk struct {
				Done               bool                `json:"done"`
				Message            ollamaChatMessageIn `json:"message"`
				ollamaChatResponse                     // usage fields
			}
			if err := dec.Decode(&chunk); err != nil {
				if err == io.EOF {
					return
				}
				ch <- StreamEvent{Type: StreamError, Err: err}
				return
			}

			if chunk.Message.Content != "" {
				ch <- StreamEvent{Type: StreamToken, Text: chunk.Message.Content}
			}

			for _, tc := range chunk.Message.ToolCalls {
				id := fmt.Sprintf("ollama-call-%d", toolCallIdx+1)
				if tc.Function.Name != "" {
					ch <- StreamEvent{
						Type:         StreamToolCallStart,
						ToolCallID:   id,
						ToolCallName: tc.Function.Name,
					}
				}
				if tc.Function.Arguments != nil {
					ch <- StreamEvent{
						Type:         StreamToolCallDelta,
						ToolCallID:   id,
						ToolCallArgs: string(tc.Function.Arguments),
					}
				}
				toolCallIdx++
			}

			if chunk.Done {
				ch <- StreamEvent{
					Type: StreamDone,
					Usage: Usage{
						InputTokens:  chunk.PromptEval,
						OutputTokens: chunk.EvalCount,
					},
				}
				return
			}
		}
	}()

	return ch, nil
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Tools    []ollamaToolDef     `json:"tools,omitempty"`
	Stream   bool                `json:"stream"`
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatMessage struct {
	Role      string              `json:"role"`
	Content   string              `json:"content,omitempty"`
	Name      string              `json:"name,omitempty"`
	ToolCalls []ollamaToolCallOut `json:"tool_calls,omitempty"`
}

type ollamaToolDef struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ollamaChatResponse struct {
	Message    ollamaChatMessageIn `json:"message"`
	PromptEval int                 `json:"prompt_eval_count"`
	EvalCount  int                 `json:"eval_count"`
}

type ollamaChatMessageIn struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	ToolCalls []ollamaToolCallIn `json:"tool_calls"`
}

type ollamaToolCallOut struct {
	Function ollamaToolCallFunctionOut `json:"function"`
}

type ollamaToolCallFunctionOut struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ollamaToolCallIn struct {
	Function ollamaToolCallFunctionIn `json:"function"`
}

type ollamaToolCallFunctionIn struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (p *OllamaProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.defaultModel
	}

	messages := make([]ollamaChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := ollamaChatMessage{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]ollamaToolCallOut, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ollamaToolCallOut{
					Function: ollamaToolCallFunctionOut{
						Name:      tc.Name,
						Arguments: tc.Args,
					},
				})
			}
		}
		messages = append(messages, msg)
	}

	tools := make([]ollamaToolDef, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, ollamaToolDef{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	options := map[string]any{}
	if req.GenParams.Temperature != nil {
		options["temperature"] = *req.GenParams.Temperature
	}
	if req.GenParams.TopP != nil {
		options["top_p"] = *req.GenParams.TopP
	}
	if req.GenParams.TopK != nil {
		options["top_k"] = *req.GenParams.TopK
	}
	if req.GenParams.Seed != nil {
		options["seed"] = *req.GenParams.Seed
	}
	if req.GenParams.MaxTokens > 0 {
		options["num_predict"] = req.GenParams.MaxTokens
	}
	if len(req.GenParams.StopSequences) > 0 {
		options["stop"] = req.GenParams.StopSequences
	}

	oReq := ollamaChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}
	if len(options) > 0 {
		oReq.Options = options
	}
	body, err := json.Marshal(oReq)
	if err != nil {
		return CompleteResponse{}, err
	}

	url := p.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuth(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("ollama: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		baseErr := fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, string(raw))
		if isRetryableStatus(resp.StatusCode) {
			return CompleteResponse{}, &RetryableError{
				Err:        baseErr,
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfterFromHeader(resp.Header.Get("Retry-After")),
			}
		}
		return CompleteResponse{}, baseErr
	}

	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompleteResponse{}, fmt.Errorf("ollama: decode: %w", err)
	}

	toolCalls := make([]ToolCall, 0, len(parsed.Message.ToolCalls))
	for i, tc := range parsed.Message.ToolCalls {
		id := fmt.Sprintf("ollama-call-%d", i+1)
		toolCalls = append(toolCalls, ToolCall{
			ID:   id,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	return CompleteResponse{
		AssistantText: parsed.Message.Content,
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  parsed.PromptEval,
			OutputTokens: parsed.EvalCount,
			CostUSD:      0,
		},
		Raw: raw,
	}, nil
}

func (p *OllamaProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.defaultModel
	}

	payload := map[string]any{
		"model":  model,
		"prompt": req.Text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return EmbedResponse{}, err
	}

	url := p.baseURL + "/api/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return EmbedResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuth(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("ollama: embed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		baseErr := fmt.Errorf("ollama: embed HTTP %d: %s", resp.StatusCode, string(raw))
		if isRetryableStatus(resp.StatusCode) {
			return EmbedResponse{}, &RetryableError{
				Err:        baseErr,
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfterFromHeader(resp.Header.Get("Retry-After")),
			}
		}
		return EmbedResponse{}, baseErr
	}

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return EmbedResponse{}, fmt.Errorf("ollama: embed decode: %w", err)
	}

	return EmbedResponse{
		Embedding: result.Embedding,
		Usage:     Usage{InputTokens: 0, OutputTokens: 0, CostUSD: 0},
	}, nil
}

func (p *OllamaProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}

	payload := map[string]any{"model": model}
	body, err := json.Marshal(payload)
	if err != nil {
		return ModelMetadata{}, err
	}

	url := p.baseURL + "/api/show"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ModelMetadata{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuth(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return ModelMetadata{}, fmt.Errorf("ollama: show: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ModelMetadata{Model: model, IsFree: true, ContextSize: 4096}, nil // fallback
	}

	var result struct {
		ModelInfo struct {
			ContextLength int `json:"context_length"`
		} `json:"model_info"`
		Details struct {
			ContextLength int `json:"context_length"`
		} `json:"details"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	ctxSize := result.ModelInfo.ContextLength
	if ctxSize == 0 {
		ctxSize = result.Details.ContextLength
	}
	if ctxSize == 0 {
		ctxSize = 4096 // default fallback
	}

	return ModelMetadata{
		Model:       model,
		ContextSize: ctxSize,
		IsFree:      true,
	}, nil
}

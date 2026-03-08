package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAIProvider implements Provider using the OpenAI API.
type OpenAIProvider struct {
	client       *openai.Client
	defaultModel string
}

// NewOpenAIProvider creates an OpenAI provider.
// authEnv is the name of the environment variable holding the API key.
// baseURL may be empty to use the default OpenAI endpoint.
func NewOpenAIProvider(authEnv, baseURL, defaultModel string) (*OpenAIProvider, error) {
	apiKey := os.Getenv(authEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("openai: env var %q is not set", authEnv)
	}

	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	client := openai.NewClientWithConfig(cfg)

	model := defaultModel
	if model == "" {
		model = openai.GPT4o
	}

	return &OpenAIProvider{client: client, defaultModel: model}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: true, Streaming: true}
}

func (p *OpenAIProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	// Build messages
	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			tcs := make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				tcs = append(tcs, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Args),
					},
				})
			}
			msg.ToolCalls = tcs
		}
		if m.Role == "tool" {
			msg.ToolCallID = m.ToolCallID
			msg.Name = m.Name
		}
		msgs = append(msgs, msg)
	}

	// Build tool definitions
	var tools []openai.Tool
	for _, ts := range req.Tools {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        ts.Name,
				Description: ts.Description,
				Parameters:  ts.InputSchema,
			},
		})
	}

	creq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: msgs,
	}
	if len(tools) > 0 {
		creq.Tools = tools
	}
	if req.GenParams.Temperature != nil {
		creq.Temperature = float32(*req.GenParams.Temperature)
	}
	if req.GenParams.TopP != nil {
		creq.TopP = float32(*req.GenParams.TopP)
	}
	if req.GenParams.MaxTokens > 0 {
		creq.MaxTokens = req.GenParams.MaxTokens
	}
	if req.GenParams.Seed != nil {
		creq.Seed = req.GenParams.Seed
	}
	if len(req.GenParams.StopSequences) > 0 {
		creq.Stop = req.GenParams.StopSequences
	}

	resp, err := p.client.CreateChatCompletion(ctx, creq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("openai: complete: %w", err)
	}

	if len(resp.Choices) == 0 {
		return CompleteResponse{}, fmt.Errorf("openai: no choices in response")
	}

	choice := resp.Choices[0]
	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: json.RawMessage(tc.Function.Arguments),
		})
	}

	// Compute approximate cost (gpt-4o pricing as fallback; callers may override)
	costUSD := estimateCost(model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)

	// Raw response — redact the auth key before storing (key is not in resp, but just to be safe)
	rawBytes, _ := json.Marshal(resp)

	return CompleteResponse{
		AssistantText: choice.Message.Content,
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			CostUSD:      costUSD,
		},
		Raw: rawBytes,
	}, nil
}

func (p *OpenAIProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	ereq := openai.EmbeddingRequest{
		Input: []string{req.Text},
		Model: openai.EmbeddingModel(model),
	}

	resp, err := p.client.CreateEmbeddings(ctx, ereq)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("openai: embed: %w", err)
	}

	if len(resp.Data) == 0 {
		return EmbedResponse{}, fmt.Errorf("openai: no embedding data in response")
	}

	return EmbedResponse{
		Embedding: resp.Data[0].Embedding,
		Usage: Usage{
			InputTokens: resp.Usage.PromptTokens,
			CostUSD:     (float64(resp.Usage.TotalTokens) / 1_000_000) * 0.02, // approx for 3-small
		},
	}, nil
}

func (p *OpenAIProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openai.ChatCompletionMessage{Role: m.Role, Content: m.Content, Name: m.Name}
		if m.Role == "tool" {
			msg.ToolCallID = m.ToolCallID
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:       tc.ID,
					Type:     openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: tc.Name, Arguments: string(tc.Args)},
				})
			}
		}
		msgs = append(msgs, msg)
	}

	var tools []openai.Tool
	for _, ts := range req.Tools {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        ts.Name,
				Description: ts.Description,
				Parameters:  ts.InputSchema,
			},
		})
	}

	oReq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	}
	if req.GenParams.Temperature != nil {
		oReq.Temperature = float32(*req.GenParams.Temperature)
	}
	if req.GenParams.TopP != nil {
		oReq.TopP = float32(*req.GenParams.TopP)
	}
	if req.GenParams.MaxTokens > 0 {
		oReq.MaxTokens = req.GenParams.MaxTokens
	}
	if len(req.GenParams.StopSequences) > 0 {
		oReq.Stop = req.GenParams.StopSequences
	}
	if req.GenParams.Seed != nil {
		oReq.Seed = req.GenParams.Seed
	}

	stream, err := p.client.CreateChatCompletionStream(ctx, oReq)
	if err != nil {
		return nil, fmt.Errorf("openai: stream: %w", err)
	}

	ch := make(chan StreamEvent, 100)
	go func() {
		defer close(ch)
		defer stream.Close()

		for {
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					ch <- StreamEvent{Type: StreamDone}
					return
				}
				ch <- StreamEvent{Type: StreamError, Err: err}
				return
			}

			if len(resp.Choices) == 0 {
				continue
			}
			choice := resp.Choices[0]

			// Text delta
			if choice.Delta.Content != "" {
				ch <- StreamEvent{Type: StreamToken, Text: choice.Delta.Content}
			}

			// Tool call deltas
			for _, tc := range choice.Delta.ToolCalls {
				if tc.Function.Name != "" {
					ch <- StreamEvent{
						Type:         StreamToolCallStart,
						ToolCallID:   tc.ID,
						ToolCallName: tc.Function.Name,
					}
				}
				if tc.Function.Arguments != "" {
					ch <- StreamEvent{
						Type:         StreamToolCallDelta,
						ToolCallID:   tc.ID,
						ToolCallArgs: tc.Function.Arguments,
					}
				}
			}

			// Usage (only available in some models/responses)
			if resp.Usage != nil {
				ch <- StreamEvent{
					Type: StreamDone,
					Usage: Usage{
						InputTokens:  resp.Usage.PromptTokens,
						OutputTokens: resp.Usage.CompletionTokens,
						CostUSD:      estimateCost(model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens),
					},
				}
				return
			}
		}
	}()

	return ch, nil
}

// estimateCost returns a rough USD cost estimate.
func estimateCost(model string, input, output int) float64 {
	// Per-1M token prices (as of early 2025 — approximate)
	var inPrice, outPrice float64
	switch model {
	case "gpt-4o", "gpt-4o-2024-08-06":
		inPrice, outPrice = 2.50, 10.00
	case "gpt-4o-mini":
		inPrice, outPrice = 0.15, 0.60
	case "gpt-4-turbo", "gpt-4-turbo-2024-04-09":
		inPrice, outPrice = 10.00, 30.00
	default:
		inPrice, outPrice = 2.50, 10.00
	}
	return (float64(input)/1_000_000)*inPrice + (float64(output)/1_000_000)*outPrice
}

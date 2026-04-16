package providers

import (
	"context"
	"fmt"
	"strings"
)

const (
	mistralBaseURL      = "https://api.mistral.ai/v1"
	mistralDefaultModel = "mistral-large-latest"
)

// MistralProvider implements Provider using the Mistral AI API.
type MistralProvider struct {
	*OpenAIProvider
}

// NewMistralProvider creates a new Mistral provider. If authEnv is empty, uses MISTRAL_API_KEY.
func NewMistralProvider(authEnv, model string) (*MistralProvider, error) {
	if authEnv == "" {
		authEnv = "MISTRAL_API_KEY"
	}
	if model == "" {
		model = mistralDefaultModel
	}

	base, err := NewOpenAIProvider(authEnv, mistralBaseURL, model)
	if err != nil {
		return nil, fmt.Errorf("mistral: %w", err)
	}

	return &MistralProvider{OpenAIProvider: base}, nil
}

func (p *MistralProvider) Name() string { return "mistral" }

func (p *MistralProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	resp, err := p.OpenAIProvider.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	resp.Usage.CostUSD = estimateMistralCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return resp, nil
}

func (p *MistralProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	ch, err := p.OpenAIProvider.StreamComplete(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamEvent, 100)
	go func() {
		defer close(out)
		model := req.Model
		if model == "" {
			model = p.defaultModel
		}
		for ev := range ch {
			if ev.Type == StreamDone {
				ev.Usage.CostUSD = estimateMistralCost(model, ev.Usage.InputTokens, ev.Usage.OutputTokens)
			}
			out <- ev
		}
	}()

	return out, nil
}

func (p *MistralProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}

	m := ModelMetadata{Model: model, ContextSize: 128000}

	switch {
	case strings.HasPrefix(model, "mistral-large"):
		m.CostPer1MIn = 2.00
		m.CostPer1MOut = 6.00
	case strings.HasPrefix(model, "mistral-medium"):
		m.CostPer1MIn = 0.80
		m.CostPer1MOut = 2.40
	case strings.HasPrefix(model, "mistral-small"):
		m.CostPer1MIn = 0.20
		m.CostPer1MOut = 0.60
	case strings.HasPrefix(model, "mistral-tiny"):
		m.CostPer1MIn = 0.035
		m.CostPer1MOut = 0.14
	}

	return m, nil
}

func (p *MistralProvider) Models() []ModelInfo {
	return []ModelInfo{
		{Name: "mistral-large-latest", Description: "flagship — complex reasoning, coding, agentic"},
		{Name: "mistral-medium-latest", Description: "balanced — good for general tasks"},
		{Name: "mistral-small-latest", Description: "fast — efficient for lighter tasks"},
		{Name: "mistral-tiny-latest", Description: "ultra-fast — simple tasks, batch"},
	}
}

// estimateMistralCost returns a rough USD cost estimate for Mistral models.
func estimateMistralCost(model string, input, output int) float64 {
	var inPrice, outPrice float64
	switch {
	case strings.HasPrefix(model, "mistral-large"):
		inPrice, outPrice = 2.00, 6.00
	case strings.HasPrefix(model, "mistral-medium"):
		inPrice, outPrice = 0.80, 2.40
	case strings.HasPrefix(model, "mistral-small"):
		inPrice, outPrice = 0.20, 0.60
	case strings.HasPrefix(model, "mistral-tiny"):
		inPrice, outPrice = 0.035, 0.14
	default:
		inPrice, outPrice = 2.00, 6.00
	}
	return (float64(input)/1_000_000)*inPrice + (float64(output)/1_000_000)*outPrice
}
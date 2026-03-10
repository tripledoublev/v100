package providers

import (
	"context"
	"fmt"
	"strings"
)

const (
	glmBaseURL      = "https://open.bigmodel.cn/api/paas/v4/"
	glmDefaultModel = "glm-4-plus"
)

// GLMProvider implements Provider using Zhipu AI's OpenAI-compatible API.
type GLMProvider struct {
	*OpenAIProvider
}

// NewGLMProvider creates a new GLM provider.
func NewGLMProvider(authEnv, model string) (*GLMProvider, error) {
	if authEnv == "" {
		authEnv = "ZHIPU_API_KEY"
	}
	if model == "" {
		model = glmDefaultModel
	}

	base, err := NewOpenAIProvider(authEnv, glmBaseURL, model)
	if err != nil {
		return nil, fmt.Errorf("glm: %w", err)
	}

	return &GLMProvider{OpenAIProvider: base}, nil
}

func (p *GLMProvider) Name() string { return "glm" }

func (p *GLMProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	resp, err := p.OpenAIProvider.Complete(ctx, req)
	if err != nil {
		return resp, err
	}

	// Override cost with GLM pricing
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	resp.Usage.CostUSD = estimateCostGLM(model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return resp, nil
}

func (p *GLMProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	ch, err := p.OpenAIProvider.StreamComplete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Wrap the channel to fix costs on StreamDone
	out := make(chan StreamEvent, 100)
	go func() {
		defer close(out)
		model := req.Model
		if model == "" {
			model = p.defaultModel
		}
		for ev := range ch {
			if ev.Type == StreamDone {
				ev.Usage.CostUSD = estimateCostGLM(model, ev.Usage.InputTokens, ev.Usage.OutputTokens)
			}
			out <- ev
		}
	}()

	return out, nil
}

func (p *GLMProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}

	m := ModelMetadata{Model: model, ContextSize: 128000}

	switch {
	case strings.HasPrefix(model, "glm-4-plus"):
		m.CostPer1MIn = 0.007 * 1000  // ~ $7.00
		m.CostPer1MOut = 0.007 * 1000 // ~ $7.00
	case strings.HasPrefix(model, "glm-4-0520"):
		m.CostPer1MIn = 0.015 * 1000
		m.CostPer1MOut = 0.015 * 1000
	case strings.HasPrefix(model, "glm-4-air"):
		m.CostPer1MIn = 0.001 * 1000
		m.CostPer1MOut = 0.001 * 1000
	case strings.HasPrefix(model, "glm-4-flash"):
		m.IsFree = true // Flash is free for some tiers, but let's set a low cost
		m.CostPer1MIn = 0.0001 * 1000
		m.CostPer1MOut = 0.0001 * 1000
	}

	return m, nil
}

// estimateCostGLM returns a USD cost estimate for GLM models.
func estimateCostGLM(model string, input, output int) float64 {
	// Approximate pricing in USD based on CNY rates (1 USD ~ 7.2 CNY)
	// GLM-4-Plus is roughly 50 CNY per 1M tokens ($7.00)
	var pricePer1M float64 = 7.00

	switch {
	case strings.HasPrefix(model, "glm-4-plus"):
		pricePer1M = 7.00
	case strings.HasPrefix(model, "glm-4-0520"):
		pricePer1M = 15.00
	case strings.HasPrefix(model, "glm-4-air"):
		pricePer1M = 1.00
	case strings.HasPrefix(model, "glm-4-flash"):
		pricePer1M = 0.10
	}

	return (float64(input+output) / 1_000_000) * pricePer1M
}

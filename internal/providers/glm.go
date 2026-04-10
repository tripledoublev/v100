package providers

import (
	"context"
	"fmt"
)

const (
	glmBaseURL      = "https://api.z.ai/api/coding/paas/v4"
	glmDefaultModel = "glm-4.7"
)

// GLMProvider implements Provider using Zhipu AI's OpenAI-compatible API.
type GLMProvider struct {
	*OpenAIProvider
}

// NewGLMProvider creates a new GLM provider. If baseURL is empty, uses the default.
func NewGLMProvider(authEnv, baseURL, model string) (*GLMProvider, error) {
	if authEnv == "" {
		authEnv = "ZHIPU_API_KEY"
	}
	if baseURL == "" {
		baseURL = glmBaseURL
	}
	if model == "" {
		model = glmDefaultModel
	}

	base, err := NewOpenAIProvider(authEnv, baseURL, model)
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

	m := ModelMetadata{Model: model, ContextSize: 128000, IsFree: true}

	// For the GLM Coding Plan, all models are part of subscription (no per-token cost)
	return m, nil
}

func (p *GLMProvider) Models() []ModelInfo {
	return []ModelInfo{
		{Name: "glm-5.1", Description: "flagship — long-horizon agents"},
		{Name: "glm-5", Description: "powerful — agentic + coding"},
		{Name: "glm-4.7", Description: "standard — enhanced coding + reasoning"},
		{Name: "glm-4.7-flashx", Description: "fast — lightweight, paid"},
		{Name: "glm-4.7-flash", Description: "free — lightweight"},
		{Name: "glm-4.6", Description: "standard — comparable to Sonnet"},
		{Name: "glm-4.5", Description: "reasoning — 355B params"},
		{Name: "glm-4.5-air", Description: "reasoning — lightweight 106B"},
		{Name: "glm-4.5-airx", Description: "reasoning — lightweight + fast"},
		{Name: "glm-4.5-flash", Description: "reasoning — free"},
	}
}

// estimateCostGLM returns a USD cost estimate for GLM models.
// For the Coding Plan, all models are included in subscription (no per-token cost).
func estimateCostGLM(model string, input, output int) float64 {
	// GLM Coding Plan models are subscription-based, no per-token charges
	return 0.0
}

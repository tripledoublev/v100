package providers

import (
	"context"
	"fmt"
	"strings"
)

const (
	glmBaseURL      = "https://api.z.ai/api/coding/paas/v4"
	glmDefaultModel = "GLM-4.7"
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

	// For the GLM Coding Plan, models are part of subscription (no per-token cost)
	switch {
	case strings.HasPrefix(model, "GLM-5") || strings.HasPrefix(model, "glm-5"):
		// GLM-5-Turbo, GLM-5.1, etc. — latest models, included in subscription
	case strings.HasPrefix(model, "GLM-4.7") || strings.HasPrefix(model, "glm-4.7"):
		// GLM-4.7 — primary model, included in subscription
	case strings.HasPrefix(model, "GLM-4.5-air") || strings.HasPrefix(model, "glm-4.5-air"):
		// GLM-4.5-air — lightweight, included in subscription
	}

	return m, nil
}

// estimateCostGLM returns a USD cost estimate for GLM models.
// For the Coding Plan, all models are included in subscription (no per-token cost).
func estimateCostGLM(model string, input, output int) float64 {
	// GLM Coding Plan models are subscription-based, no per-token charges
	return 0.0
}

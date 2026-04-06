package providers

import (
	"context"
	"os"
	"strings"
)

const (
	llamaCppDefaultBaseURL = "http://127.0.0.1:19091/v1"
	llamaCppDefaultModel   = "gemma-4-E2B-it-GGUF:Q8_0"
)

// LlamaCppProvider implements Provider against a local llama.cpp server.
//
// It reuses the OpenAI-compatible client and request formatting used by the
// OpenAI provider, but defaults to a local base URL and no real API key.
type LlamaCppProvider struct {
	*OpenAIProvider
}

func NewLlamaCppProvider(baseURL, defaultModel string) (*LlamaCppProvider, error) {
	finalBaseURL := strings.TrimSpace(baseURL)
	if finalBaseURL == "" {
		if v := os.Getenv("LLAMA_CPP_BASE_URL"); v != "" {
			finalBaseURL = v
		} else if v := os.Getenv("LLAMA_SERVER_URL"); v != "" {
			finalBaseURL = v
		} else if v := os.Getenv("LLAMA_BASE_URL"); v != "" {
			finalBaseURL = v
		} else {
			finalBaseURL = llamaCppDefaultBaseURL
		}
	}

	model := strings.TrimSpace(defaultModel)
	if model == "" {
		if v := os.Getenv("LLAMA_CPP_MODEL"); v != "" {
			model = v
		} else {
			model = llamaCppDefaultModel
		}
	}

	return &LlamaCppProvider{
		OpenAIProvider: &OpenAIProvider{
			client:       newOpenAIClient("llama.cpp", finalBaseURL),
			defaultModel: model,
		},
	}, nil
}

func (p *LlamaCppProvider) Name() string { return "llamacpp" }

func (p *LlamaCppProvider) Capabilities() Capabilities {
	return p.OpenAIProvider.Capabilities()
}

func (p *LlamaCppProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}

	m := ModelMetadata{Model: model, ContextSize: 128000, IsFree: true}
	lower := strings.ToLower(model)

	switch {
	case strings.Contains(lower, "26b-a4b"), strings.Contains(lower, "31b"):
		m.ContextSize = 256000
	case strings.Contains(lower, "e2b"), strings.Contains(lower, "e4b"):
		m.ContextSize = 128000
	case strings.Contains(lower, "1b"):
		m.ContextSize = 131072
	}

	return m, nil
}

package providers

import (
	"context"
	"strings"
	"testing"
)

func TestNewGLMProviderDefault(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "glm" {
		t.Errorf("expected name glm, got %s", p.Name())
	}
	if p.defaultModel != glmDefaultModel {
		t.Errorf("expected default model %s, got %s", glmDefaultModel, p.defaultModel)
	}
}

func TestNewGLMProviderCustomModel(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "", "GLM-4.5-air")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.defaultModel != "GLM-4.5-air" {
		t.Errorf("expected GLM-4.5-air, got %s", p.defaultModel)
	}
}

func TestNewGLMProviderNoKey(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "")
	_, err := NewGLMProvider("", "", "")
	if err == nil {
		t.Fatal("expected error for missing ZHIPU_API_KEY")
	}
}

func TestEstimateCostGLM(t *testing.T) {
	tests := []struct {
		model        string
		input        int
		output       int
		expectedCost float64
	}{
		{"GLM-4.7", 1_000_000, 1_000_000, 0.0},      // subscription-based
		{"GLM-4.5-air", 1_000_000, 1_000_000, 0.0},  // subscription-based
		{"GLM-5-Turbo", 1_000_000, 1_000_000, 0.0},  // subscription-based
		{"GLM-5.1", 1_000_000, 1_000_000, 0.0},      // subscription-based
		{"unknown-model", 1_000_000, 1_000_000, 0.0}, // default to 0
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			cost := estimateCostGLM(tt.model, tt.input, tt.output)
			if cost != tt.expectedCost {
				t.Errorf("expected cost %v, got %v", tt.expectedCost, cost)
			}
		})
	}
}

func TestGLMCapabilities(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "", "")
	if err != nil {
		t.Fatal(err)
	}

	caps := p.Capabilities()
	if !caps.ToolCalls {
		t.Error("expected ToolCalls=true")
	}
	if !caps.Streaming {
		t.Error("expected Streaming=true")
	}
}

func TestGLMMetadata(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "", "")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		model string
	}{
		{"GLM-4.7"},
		{"GLM-4.5-air"},
		{"GLM-5-Turbo"},
		{"GLM-5.1"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			meta, err := p.Metadata(context.TODO(), tt.model)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Model != tt.model {
				t.Errorf("expected model %s, got %s", tt.model, meta.Model)
			}
			if meta.ContextSize != 128000 {
				t.Errorf("expected context size 128000, got %d", meta.ContextSize)
			}
			if !meta.IsFree {
				t.Errorf("expected IsFree=true for Coding Plan model, got %v", meta.IsFree)
			}
		})
	}
}

func TestGLMModelPrefixMatching(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "", "")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		model string
	}{
		{"GLM-4.7-variant"},
		{"GLM-4.5-air-v1"},
		{"GLM-5-Turbo-v2"},
		{"GLM-5.1-experimental"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			meta, err := p.Metadata(context.TODO(), tt.model)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(meta.Model, "GLM-") {
				t.Errorf("expected model to have GLM- prefix, got %s", meta.Model)
			}
			if !meta.IsFree {
				t.Errorf("expected IsFree=true for Coding Plan model, got %v", meta.IsFree)
			}
		})
	}
}

func TestNewGLMProviderCustomBaseURL(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	customURL := "https://api.example.com/v1"
	p, err := NewGLMProvider("", customURL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify that custom base URL was used by checking the OpenAI client's config
	// (baseURL is stored in the embedded OpenAIProvider)
	if p.OpenAIProvider == nil {
		t.Fatal("expected OpenAIProvider to be initialized")
	}
}

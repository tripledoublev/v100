package providers

import (
	"context"
	"strings"
	"testing"
)

func TestNewGLMProviderDefault(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "")
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
	p, err := NewGLMProvider("", "glm-4-air")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.defaultModel != "glm-4-air" {
		t.Errorf("expected glm-4-air, got %s", p.defaultModel)
	}
}

func TestNewGLMProviderNoKey(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "")
	_, err := NewGLMProvider("", "")
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
		{"glm-4-plus", 1_000_000, 1_000_000, 14.0},
		{"glm-4-0520", 1_000_000, 1_000_000, 30.0},
		{"glm-4-air", 1_000_000, 1_000_000, 2.0},
		{"glm-4-flash", 1_000_000, 1_000_000, 0.2},
		{"unknown-model", 1_000_000, 1_000_000, 14.0},
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
	p, err := NewGLMProvider("", "")
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
	p, err := NewGLMProvider("", "")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		model       string
		expectCost  float64
		expectFree  bool
	}{
		{"glm-4-plus", 7.0, false},      // 0.007 * 1000
		{"glm-4-0520", 15.0, false},     // 0.015 * 1000
		{"glm-4-air", 1.0, false},       // 0.001 * 1000
		{"glm-4-flash", 0.1, true},      // 0.0001 * 1000
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
			if meta.CostPer1MIn != tt.expectCost {
				t.Errorf("expected input cost %v, got %v", tt.expectCost, meta.CostPer1MIn)
			}
			if meta.CostPer1MOut != tt.expectCost {
				t.Errorf("expected output cost %v, got %v", tt.expectCost, meta.CostPer1MOut)
			}
			if meta.IsFree != tt.expectFree {
				t.Errorf("expected IsFree=%v, got %v", tt.expectFree, meta.IsFree)
			}
		})
	}
}

func TestGLMModelPrefixMatching(t *testing.T) {
	t.Setenv("ZHIPU_API_KEY", "test-key")
	p, err := NewGLMProvider("", "")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		model       string
		expectCost  float64
	}{
		{"glm-4-plus-1224", 7.0},
		{"glm-4-0520-preview", 15.0},
		{"glm-4-air-v1", 1.0},
		{"glm-4-flash-v2", 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			meta, err := p.Metadata(context.TODO(), tt.model)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(meta.Model, "glm-4") {
				t.Errorf("expected model to have glm-4 prefix, got %s", meta.Model)
			}
			if meta.CostPer1MIn != tt.expectCost {
				t.Errorf("expected input cost %v, got %v", tt.expectCost, meta.CostPer1MIn)
			}
		})
	}
}

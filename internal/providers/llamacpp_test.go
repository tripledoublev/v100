package providers

import (
	"context"
	"testing"
)

func TestNewLlamaCppProvider(t *testing.T) {
	p, err := NewLlamaCppProvider("http://127.0.0.1:19091/v1", "gemma-4-E2B-it-GGUF:Q8_0")
	if err != nil {
		t.Fatalf("NewLlamaCppProvider() error = %v", err)
	}

	if got := p.Name(); got != "llamacpp" {
		t.Fatalf("Name() = %q, want llamacpp", got)
	}

	if got := p.Capabilities(); got != (Capabilities{ToolCalls: true, JSONMode: true, Streaming: true, Images: true}) {
		t.Fatalf("Capabilities() = %+v, want OpenAI-compatible capabilities", got)
	}

	meta, err := p.Metadata(context.TODO(), "")
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	if meta.Model != "gemma-4-E2B-it-GGUF:Q8_0" {
		t.Fatalf("Metadata().Model = %q, want default model", meta.Model)
	}
	if !meta.IsFree {
		t.Fatal("Metadata().IsFree = false, want true")
	}
	if meta.ContextSize != 128000 {
		t.Fatalf("Metadata().ContextSize = %d, want 128000", meta.ContextSize)
	}
}

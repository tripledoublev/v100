package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

func TestComputeStatsCapturesModelMetadata(t *testing.T) {
	payload, err := json.Marshal(RunStartPayload{
		Provider: "codex",
		Model:    "gpt-5.4",
		ModelMetadata: providers.ModelMetadata{
			Model:       "gpt-5.4",
			ContextSize: 128000,
			IsFree:      true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := ComputeStats([]Event{{
		TS:      time.Unix(0, 0),
		RunID:   "run-1",
		Type:    EventRunStart,
		Payload: payload,
	}})

	if stats.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", stats.Provider)
	}
	if stats.ModelMetadata.ContextSize != 128000 {
		t.Fatalf("context size = %d, want 128000", stats.ModelMetadata.ContextSize)
	}
	if !stats.ModelMetadata.IsFree {
		t.Fatal("expected free model metadata to be captured")
	}
}

func TestFormatStatsIncludesMetadata(t *testing.T) {
	out := FormatStats(RunStats{
		RunID:         "run-1",
		Provider:      "openai",
		Model:         "gpt-4.1",
		ModelMetadata: providers.ModelMetadata{ContextSize: 128000, CostPer1MIn: 2.5, CostPer1MOut: 10},
	})

	for _, want := range []string{
		"Context:      128k",
		"Pricing:      $2.50/$10.00",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatStats() missing %q in output:\n%s", want, out)
		}
	}
}

func TestFormatCompareIncludesMetadata(t *testing.T) {
	out := FormatCompare([]RunStats{
		{
			RunID:         "run-1",
			Provider:      "codex",
			Model:         "gpt-5.4",
			ModelMetadata: providers.ModelMetadata{ContextSize: 128000, IsFree: true},
		},
		{
			RunID:         "run-2",
			Provider:      "openai",
			Model:         "gpt-4.1",
			ModelMetadata: providers.ModelMetadata{ContextSize: 128000, CostPer1MIn: 2.5, CostPer1MOut: 10},
		},
	})

	for _, want := range []string{
		"Provider",
		"Model",
		"Context",
		"Pricing",
		"free",
		"$2.50/$10.00",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatCompare() missing %q in output:\n%s", want, out)
		}
	}
}

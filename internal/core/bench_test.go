package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBenchConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")

	toml := `
name = "test-bench"

[[prompts]]
message = "What is 2+2?"
expected = "4"
scorer = "contains"

[[prompts]]
message = "Name a color"

[[variants]]
name = "fast"
provider = "openai"
model = "gpt-4o-mini"
budget_steps = 5
temperature = 0.5
seed = 42

[[variants]]
name = "default"
provider = "openai"
model = "gpt-4o"
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	bc, err := LoadBenchConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if bc.Name != "test-bench" {
		t.Errorf("expected name test-bench, got %s", bc.Name)
	}
	if len(bc.Prompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(bc.Prompts))
	}
	if bc.Prompts[0].Expected != "4" {
		t.Errorf("expected '4', got %s", bc.Prompts[0].Expected)
	}
	if bc.Prompts[0].Scorer != "contains" {
		t.Errorf("expected scorer 'contains', got %s", bc.Prompts[0].Scorer)
	}
	if len(bc.Variants) != 2 {
		t.Errorf("expected 2 variants, got %d", len(bc.Variants))
	}
	if bc.Variants[0].Temperature == nil || *bc.Variants[0].Temperature != 0.5 {
		t.Error("expected temperature 0.5 on first variant")
	}
	if bc.Variants[0].Seed == nil || *bc.Variants[0].Seed != 42 {
		t.Error("expected seed 42 on first variant")
	}
	if bc.Variants[1].Temperature != nil {
		t.Error("expected nil temperature on second variant")
	}
}

func TestLoadBenchConfigNoPrompts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")
	if err := os.WriteFile(path, []byte(`name = "empty"
[[variants]]
name = "v1"
provider = "openai"
model = "gpt-4o"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBenchConfig(path)
	if err == nil {
		t.Error("expected error for no prompts")
	}
}

func TestLoadBenchConfigNoVariants(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench.toml")
	if err := os.WriteFile(path, []byte(`name = "empty"
[[prompts]]
message = "test"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBenchConfig(path)
	if err == nil {
		t.Error("expected error for no variants")
	}
}

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestPersistModelMetadataUpdatesMeta(t *testing.T) {
	runDir := t.TempDir()
	if err := core.WriteMeta(runDir, core.RunMeta{
		RunID:     "run-1",
		Provider:  "codex",
		Model:     "gpt-5.4",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	metadata := providers.ModelMetadata{
		Model:       "gpt-5.4",
		ContextSize: 128000,
		IsFree:      true,
	}
	persistModelMetadata(runDir, metadata)

	meta, err := core.ReadMeta(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ModelMetadata != metadata {
		t.Fatalf("model metadata = %+v, want %+v", meta.ModelMetadata, metadata)
	}
}

func TestPersistRunSelectionUpdatesProviderModelAndClearsMetadata(t *testing.T) {
	runDir := t.TempDir()
	original := providers.ModelMetadata{Model: "old-model", ContextSize: 64000, IsFree: true}
	if err := core.WriteMeta(runDir, core.RunMeta{
		RunID:         "run-1",
		Provider:      "minimax",
		Model:         "old-model",
		ModelMetadata: original,
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	persistRunSelection(runDir, "codex", "gpt-5.4", providers.ModelMetadata{}, true)

	meta, err := core.ReadMeta(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", meta.Provider)
	}
	if meta.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", meta.Model)
	}
	if meta.ModelMetadata != (providers.ModelMetadata{}) {
		t.Fatalf("model metadata = %+v, want cleared metadata", meta.ModelMetadata)
	}

	updated := providers.ModelMetadata{Model: "gpt-5.4", ContextSize: 256000}
	persistRunSelection(runDir, "codex", "gpt-5.4", updated, false)

	meta, err = core.ReadMeta(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ModelMetadata != updated {
		t.Fatalf("model metadata = %+v, want %+v", meta.ModelMetadata, updated)
	}
	if meta.Provider != "codex" || meta.Model != "gpt-5.4" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.4", meta.Provider, meta.Model)
	}
	if meta.CreatedAt.IsZero() {
		t.Fatal("created_at should be preserved")
	}
	if meta.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", meta.RunID)
	}
}

func TestQueryCmdShowsModelMetadata(t *testing.T) {
	root := t.TempDir()
	runsDir := filepath.Join(root, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(runsDir, "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteMeta(runDir, core.RunMeta{
		RunID:     "run-1",
		Name:      "demo",
		Provider:  "codex",
		Model:     "gpt-5.4",
		CreatedAt: time.Now().UTC(),
		ModelMetadata: providers.ModelMetadata{
			Model:       "gpt-5.4",
			ContextSize: 128000,
			IsFree:      true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := withWorkingDir(root, func() error {
		out, err := captureStdout(func() error {
			cmd := queryCmd()
			return cmd.RunE(cmd, nil)
		})
		if err != nil {
			return err
		}
		for _, want := range []string{"run-1", "codex", "gpt-5.4", "128k", "free", "demo"} {
			if !strings.Contains(out, want) {
				t.Fatalf("query output missing %q in:\n%s", want, out)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	runErr := fn()
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		return "", readErr
	}
	return string(out), runErr
}

func TestBenchCmdPreservesLegacyDirectUsage(t *testing.T) {
	cfgPath := ""
	cmd := benchCmd(&cfgPath)
	if cmd.Args == nil {
		t.Fatal("bench command should validate args")
	}
	if err := cmd.Args(cmd, []string{"demo.bench.toml"}); err != nil {
		t.Fatalf("legacy bench invocation should accept a direct bench file: %v", err)
	}
	if cmd.RunE == nil {
		t.Fatal("bench command should preserve direct RunE support for legacy usage")
	}
}

func TestBenchBootstrapRefusesOverwriteWithoutForce(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		filename := "demo.bench.toml"
		if err := os.WriteFile(filename, []byte("original"), 0o644); err != nil {
			return err
		}

		cmd := benchBootstrapCmd()
		err := cmd.RunE(cmd, []string{"demo"})
		if err == nil {
			t.Fatal("expected bootstrap overwrite to fail without --force")
		}
		if !strings.Contains(err.Error(), "use --force to overwrite") {
			t.Fatalf("unexpected error: %v", err)
		}

		data, readErr := os.ReadFile(filename)
		if readErr != nil {
			return readErr
		}
		if string(data) != "original" {
			t.Fatalf("bootstrap should not overwrite existing file without --force, got %q", string(data))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBenchBootstrapForceOverwritesWithValidScaffold(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		filename := "demo.bench.toml"
		if err := os.WriteFile(filename, []byte("original"), 0o644); err != nil {
			return err
		}

		cmd := benchBootstrapCmd()
		if err := cmd.Flags().Set("force", "true"); err != nil {
			return err
		}
		if err := cmd.Flags().Set("provider", "gemini"); err != nil {
			return err
		}
		if err := cmd.Flags().Set("solver", "react"); err != nil {
			return err
		}

		if _, err := captureStdout(func() error {
			return cmd.RunE(cmd, []string{"demo"})
		}); err != nil {
			return err
		}

		if _, err := core.LoadBenchConfig(filename); err != nil {
			t.Fatalf("expected generated scaffold to parse, got %v", err)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		text := string(data)
		for _, want := range []string{`name = "demo"`, `provider = "gemini"`, `solver   = "react"`} {
			if !strings.Contains(text, want) {
				t.Fatalf("scaffold missing %q in:\n%s", want, text)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBenchBootstrapPathPrefersBenchmarksDirWhenPresent(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		benchDir := filepath.Join("tests", "benchmarks")
		if err := os.MkdirAll(benchDir, 0o755); err != nil {
			return err
		}
		got, err := defaultBenchBootstrapPath("demo")
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		want := filepath.Join(cwd, "tests", "benchmarks", "demo.bench.toml")
		if got != want {
			t.Fatalf("defaultBenchBootstrapPath() = %q, want %q", got, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBenchBootstrapPathFallsBackToCWDWhenBenchmarksDirMissing(t *testing.T) {
	if err := withWorkingDir(t.TempDir(), func() error {
		got, err := defaultBenchBootstrapPath("demo")
		if err != nil {
			return err
		}
		if got != "demo.bench.toml" {
			t.Fatalf("defaultBenchBootstrapPath() = %q, want %q", got, "demo.bench.toml")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

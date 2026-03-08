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

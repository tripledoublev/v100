package main

import (
	"context"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/memory"
	"github.com/tripledoublev/v100/internal/providers"
)

type fakeMemoryProvider struct{}

func (p *fakeMemoryProvider) Name() string { return "fake-memory" }
func (p *fakeMemoryProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}
func (p *fakeMemoryProvider) Complete(context.Context, providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{}, nil
}
func (p *fakeMemoryProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{Embedding: []float32{1, 0}}, nil
}
func (p *fakeMemoryProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

func TestMemoryRememberCmdStoresWorkspaceEntry(t *testing.T) {
	root := t.TempDir()
	cfgPath := ""

	oldBuilder := buildMemoryProvider
	buildMemoryProvider = func(_ *config.Config, _ string) (providers.Provider, error) {
		return &fakeMemoryProvider{}, nil
	}
	defer func() { buildMemoryProvider = oldBuilder }()

	if err := withWorkingDir(root, func() error {
		out, err := captureStdout(func() error {
			cmd := memoryCmd(&cfgPath)
			cmd.SetArgs([]string{"remember", "--tag", "topic=replay", "persist replay artifacts"})
			return cmd.Execute()
		})
		if err != nil {
			return err
		}
		if !strings.Contains(out, "stored memory entry: mem-") {
			t.Fatalf("remember output missing stored ID in:\n%s", out)
		}
		entries, err := memory.LoadAuditEntries(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Source != "workspace-memory" {
			t.Fatalf("unexpected audit entries after remember: %+v", entries)
		}
		if entries[0].Origin != "cli:remember" || entries[0].Confidence != "manual" {
			t.Fatalf("unexpected remembered entry metadata: %+v", entries[0])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

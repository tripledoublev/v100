package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestMemoryListCmdOmitsExpiredEntries(t *testing.T) {
	root := t.TempDir()
	past := time.Now().Add(-time.Hour)
	store := memory.NewWorkspaceVectorStore(root)
	if err := store.Add(memory.MemoryItem{
		ID:        "mem-expired",
		Content:   "temporary debugging note",
		Category:  "note",
		ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(memory.MemoryItem{
		ID:       "mem-live",
		Content:  "durable workspace fact",
		Category: "fact",
	}); err != nil {
		t.Fatal(err)
	}

	cfgPath := ""
	if err := withWorkingDir(root, func() error {
		out, err := captureStdout(func() error {
			cmd := memoryCmd(&cfgPath)
			cmd.SetArgs([]string{"list"})
			return cmd.Execute()
		})
		if err != nil {
			return err
		}
		if strings.Contains(out, "temporary debugging note") {
			t.Fatalf("expired entry should not be listed:\n%s", out)
		}
		if !strings.Contains(out, "durable workspace fact") {
			t.Fatalf("live entry missing from list:\n%s", out)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "blackboard.vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mem-expired") {
		t.Fatalf("expired entry should have been pruned from disk: %s", string(data))
	}
}

func TestParseDurationRejectsMalformedDays(t *testing.T) {
	if _, err := parseDuration("7xd"); err == nil {
		t.Fatal("expected malformed day duration to fail")
	}
	d, err := parseDuration("7d")
	if err != nil {
		t.Fatalf("parseDuration(7d) error = %v", err)
	}
	if d != 7*24*time.Hour {
		t.Fatalf("parseDuration(7d) = %v", d)
	}
}

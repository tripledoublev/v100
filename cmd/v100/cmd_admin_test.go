package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/memory"
)

func TestSandboxBackendNeedsDocker(t *testing.T) {
	cfg := config.DefaultConfig()
	if sandboxBackendNeedsDocker(cfg) {
		t.Fatal("expected host backend to not require docker")
	}
	cfg.Sandbox.Backend = "docker"
	if !sandboxBackendNeedsDocker(cfg) {
		t.Fatal("expected docker backend to require docker")
	}
}

func TestMemoryListCmdShowsAuditMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("- remember summaries"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := memory.NewWorkspaceVectorStore(root)
	if err := store.Add(memory.MemoryItem{
		ID:      "mem-1",
		Content: "persist replay artifacts",
		Metadata: memory.Metadata{
			Tags: map[string]string{"scope": "workspace", "origin": "tool:blackboard_store", "confidence": "stored"},
		},
		TS: time.Date(2026, 3, 24, 4, 0, 0, 0, time.UTC),
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
		for _, want := range []string{"source=MEMORY.md", "source=workspace-memory", "confidence=stored", "persist replay artifacts"} {
			if !strings.Contains(out, want) {
				t.Fatalf("memory list output missing %q in:\n%s", want, out)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConfigInitDoesNotWritePlaintextOAuthTemplate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)

	out, err := captureStdout(func() error {
		cmd := configInitCmd()
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("config init error = %v", err)
	}

	credsPath := filepath.Join(root, "v100", "oauth_credentials.json")
	if _, err := os.Stat(credsPath); !os.IsNotExist(err) {
		t.Fatalf("oauth credentials file exists or stat failed: %v", err)
	}
	if !strings.Contains(out, "OAuth client secrets not written to disk by default") {
		t.Fatalf("config init output missing secure guidance:\n%s", out)
	}
}

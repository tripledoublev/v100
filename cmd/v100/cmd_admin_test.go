package main

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestExecutorResourceStatusLineReportsResources(t *testing.T) {
	line, unhealthy, unavailable := executorResourceStatusLine()
	if runtime.GOOS == "linux" {
		if unavailable {
			t.Fatalf("resource status reported unavailable: %q", line)
		}
		for _, want := range []string{"Executor resources:", "open_fds=", "subprocesses=", "zombies=", "process_pool=", "fd_soft_limit="} {
			if !strings.Contains(line, want) {
				t.Fatalf("resource status line missing %q in %q", want, line)
			}
		}
		return
	}
	if unhealthy || !unavailable || !strings.Contains(line, "Executor resources: unavailable") {
		t.Fatalf("resource status = (%q, unhealthy=%v, unavailable=%v), want unavailable only", line, unhealthy, unavailable)
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

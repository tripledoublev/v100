package main

import (
	"archive/tar"
	"bytes"
	"io"
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

func TestAddPathToTarIncludesSnapshotTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "snap-1", "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "snap-1", ".v100snapshot.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "snap-1", "content", "blob"), []byte("data"), 0o444); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := addPathToTar(tw, root, "snapshots"); err != nil {
		t.Fatalf("addPathToTar returned error: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	tr := tar.NewReader(&buf)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[header.Name] = true
	}
	for _, want := range []string{"snapshots/", "snapshots/snap-1/", "snapshots/snap-1/.v100snapshot.json", "snapshots/snap-1/content/blob"} {
		if !seen[want] {
			t.Fatalf("tar missing %q; seen=%v", want, seen)
		}
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
		TS: time.Now().UTC(),
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

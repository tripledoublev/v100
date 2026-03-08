package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
)

func TestResolveResumeSourceWorkspace(t *testing.T) {
	runDir := t.TempDir()
	origWD := t.TempDir()

	tests := []struct {
		name            string
		workspaceFlag   string
		tracedWorkspace string
		meta            core.RunMeta
		want            string
	}{
		{
			name:          "explicit flag wins",
			workspaceFlag: filepath.Join(runDir, "flag-workspace"),
			meta:          core.RunMeta{SourceWorkspace: filepath.Join(runDir, "meta-workspace")},
			want:          filepath.Join(runDir, "flag-workspace"),
		},
		{
			name:            "meta source workspace wins over trace",
			meta:            core.RunMeta{SourceWorkspace: filepath.Join(runDir, "meta-workspace")},
			tracedWorkspace: filepath.Join(runDir, "trace-workspace"),
			want:            filepath.Join(runDir, "meta-workspace"),
		},
		{
			name:            "real traced workspace is reused",
			tracedWorkspace: filepath.Join(runDir, "trace-workspace"),
			want:            filepath.Join(runDir, "trace-workspace"),
		},
		{
			name:            "virtual traced workspace falls back to cwd",
			tracedWorkspace: "/workspace",
			want:            origWD,
		},
	}

	if err := withWorkingDir(origWD, func() error {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := resolveResumeSourceWorkspace(tt.workspaceFlag, runDir, tt.tracedWorkspace, tt.meta)
				if got != tt.want {
					t.Fatalf("resolveResumeSourceWorkspace() = %q, want %q", got, tt.want)
				}
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSandboxSessionDisabledUsesSourceWorkspace(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox.Enabled = false

	sourceDir := t.TempDir()
	session, mapper, workspace, err := buildSandboxSession(cfg, "run-1", sourceDir, t.TempDir())
	if err != nil {
		t.Fatalf("buildSandboxSession returned error: %v", err)
	}

	if workspace != sourceDir {
		t.Fatalf("workspace = %q, want %q", workspace, sourceDir)
	}
	if session.Workspace() != sourceDir {
		t.Fatalf("session.Workspace() = %q, want %q", session.Workspace(), sourceDir)
	}
	gotPath, ok := mapper.SecurePath("child.txt")
	if !ok {
		t.Fatal("expected secure relative path to be allowed")
	}
	wantPath := filepath.Join(sourceDir, "child.txt")
	if gotPath != wantPath {
		t.Fatalf("SecurePath(child.txt) = %q, want %q", gotPath, wantPath)
	}
}

func TestLoopNetworkTier(t *testing.T) {
	cfg := testConfig()

	if got := loopNetworkTier(cfg); got != "open" {
		t.Fatalf("loopNetworkTier() with sandbox disabled = %q, want open", got)
	}

	cfg.Sandbox.Enabled = true
	cfg.Sandbox.NetworkTier = ""
	if got := loopNetworkTier(cfg); got != "off" {
		t.Fatalf("loopNetworkTier() with empty sandbox network tier = %q, want off", got)
	}

	cfg.Sandbox.NetworkTier = "research"
	if got := loopNetworkTier(cfg); got != "research" {
		t.Fatalf("loopNetworkTier() with research tier = %q, want research", got)
	}
}

func TestFinalizeSandboxRunUsesStoredFingerprint(t *testing.T) {
	cfg := testConfig()
	cfg.Sandbox.Enabled = true
	cfg.Sandbox.ApplyBack = "on_success"

	runDir := t.TempDir()
	tracePath := filepath.Join(runDir, "trace.jsonl")
	sourceDir := filepath.Join(t.TempDir(), "source")
	sandboxDir := filepath.Join(t.TempDir(), "sandbox")

	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandboxDir, "file.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fingerprint, err := core.WorkspaceFingerprint(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.WriteMeta(runDir, core.RunMeta{
		RunID:             "run-1",
		SourceWorkspace:   sourceDir,
		SourceFingerprint: fingerprint,
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("source-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := finalizeSandboxRun(cfg, &core.Run{ID: "run-1", TraceFile: tracePath}, "user_exit", core.NewPathMapper(sourceDir, sandboxDir))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected sandbox finalize result")
	}
	if !result.Conflict {
		t.Fatal("expected source fingerprint mismatch to block apply-back")
	}
	if result.SkippedReason != "source_workspace_changed" {
		t.Fatalf("skipped_reason = %q, want source_workspace_changed", result.SkippedReason)
	}
}

func TestRunReasonAllowsApplyBack(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{reason: "user_exit", want: true},
		{reason: "completed", want: true},
		{reason: "budget_steps", want: false},
		{reason: "error", want: false},
	}

	for _, tt := range tests {
		if got := runReasonAllowsApplyBack(tt.reason); got != tt.want {
			t.Fatalf("runReasonAllowsApplyBack(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func testConfig() *config.Config {
	return config.DefaultConfig()
}

func withWorkingDir(dir string, fn func() error) error {
	prev, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(prev)
	}()
	return fn()
}

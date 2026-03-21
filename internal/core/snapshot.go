package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// SnapshotRequest describes a pre-mutation snapshot capture request.
type SnapshotRequest struct {
	RunID    string
	StepID   string
	CallID   string
	ToolName string
	Reason   string
}

// SnapshotResult describes the snapshot captured for a tool execution.
type SnapshotResult struct {
	ID     string
	Method string
}

// RestoreRequest describes a snapshot restore request.
type RestoreRequest struct {
	RunID      string
	StepID     string
	SnapshotID string
	Reason     string
}

// RestoreResult describes the restored snapshot state.
type RestoreResult struct {
	SnapshotID string
	Method     string
}

// SnapshotManager captures and restores sandbox state around mutating actions.
type SnapshotManager interface {
	Capture(ctx context.Context, req SnapshotRequest) (SnapshotResult, error)
	Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error)
}

// NoopSnapshotManager is the default placeholder implementation for Phase 3c wiring.
type NoopSnapshotManager struct{}

func (NoopSnapshotManager) Capture(_ context.Context, req SnapshotRequest) (SnapshotResult, error) {
	id := "noop"
	if req.CallID != "" {
		id = "noop-" + req.CallID
	}
	return SnapshotResult{ID: id, Method: "noop"}, nil
}

func (NoopSnapshotManager) Restore(_ context.Context, req RestoreRequest) (RestoreResult, error) {
	id := req.SnapshotID
	if id == "" {
		id = "noop"
	}
	return RestoreResult{SnapshotID: id, Method: "noop"}, nil
}

// WorkspaceSnapshotManager stores full-copy snapshots of a workspace tree.
type WorkspaceSnapshotManager struct {
	WorkspaceDir string
	SnapshotRoot string
	seq          uint64
}

func NewWorkspaceSnapshotManager(workspaceDir, snapshotRoot string) *WorkspaceSnapshotManager {
	return &WorkspaceSnapshotManager{
		WorkspaceDir: filepath.Clean(workspaceDir),
		SnapshotRoot: filepath.Clean(snapshotRoot),
	}
}

func (m *WorkspaceSnapshotManager) Capture(_ context.Context, req SnapshotRequest) (SnapshotResult, error) {
	if strings.TrimSpace(m.WorkspaceDir) == "" || strings.TrimSpace(m.SnapshotRoot) == "" {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: workspace and snapshot root are required")
	}
	if err := os.MkdirAll(m.SnapshotRoot, 0o755); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: mkdir root: %w", err)
	}

	id := m.nextSnapshotID(req)
	dst := filepath.Join(m.SnapshotRoot, id)
	if err := os.RemoveAll(dst); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: clear %s: %w", dst, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: mkdir %s: %w", dst, err)
	}
	if err := copyTree(m.WorkspaceDir, dst); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: capture %s: %w", id, err)
	}
	return SnapshotResult{ID: id, Method: "full_copy"}, nil
}

func (m *WorkspaceSnapshotManager) Restore(_ context.Context, req RestoreRequest) (RestoreResult, error) {
	if strings.TrimSpace(req.SnapshotID) == "" {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: snapshot id is required")
	}
	src := filepath.Join(m.SnapshotRoot, req.SnapshotID)
	if _, err := os.Stat(src); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: stat %s: %w", src, err)
	}
	if err := os.MkdirAll(m.WorkspaceDir, 0o755); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: mkdir workspace: %w", err)
	}
	if err := clearDir(m.WorkspaceDir); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: clear workspace: %w", err)
	}
	if err := copyTree(src, m.WorkspaceDir); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: restore %s: %w", req.SnapshotID, err)
	}
	return RestoreResult{SnapshotID: req.SnapshotID, Method: "full_copy"}, nil
}

func (m *WorkspaceSnapshotManager) nextSnapshotID(req SnapshotRequest) string {
	n := atomic.AddUint64(&m.seq, 1)
	parts := []string{"snap", fmt.Sprintf("%06d", n)}
	if req.StepID != "" {
		parts = append(parts, sanitizeSnapshotComponent(req.StepID))
	}
	if req.CallID != "" {
		parts = append(parts, sanitizeSnapshotComponent(req.CallID))
	}
	return strings.Join(parts, "-")
}

func sanitizeSnapshotComponent(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, string(filepath.Separator), "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	filter := newWorkspaceFilter(src)
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if filter.Skip(rel, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copySnapshotFile(path, target, info.Mode())
	})
}

func copySnapshotFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

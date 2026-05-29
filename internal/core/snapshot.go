package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func (NoopSnapshotManager) CaptureAsync(_ context.Context, req SnapshotRequest) (AsyncSnapshot, error) {
	result, err := NoopSnapshotManager{}.Capture(context.Background(), req)
	done := make(chan error)
	close(done)
	return AsyncSnapshot{Result: result, Done: done}, err
}

func (NoopSnapshotManager) Restore(_ context.Context, req RestoreRequest) (RestoreResult, error) {
	id := req.SnapshotID
	if id == "" {
		id = "noop"
	}
	return RestoreResult{SnapshotID: id, Method: "noop"}, nil
}

// SnapshotMode selects how WorkspaceSnapshotManager persists workspace state.
type SnapshotMode string

const (
	SnapshotModeDelta    SnapshotMode = "delta"
	SnapshotModeFullCopy SnapshotMode = "full_copy"
)

// WorkspaceSnapshotOptions controls snapshot storage behavior.
type WorkspaceSnapshotOptions struct {
	Mode SnapshotMode
}

// AsyncSnapshot captures the result handle and completion channel for a background snapshot.
type AsyncSnapshot struct {
	Result SnapshotResult
	Done   <-chan error
}

// WorkspaceSnapshotManager stores restorable snapshots of a workspace tree.
type WorkspaceSnapshotManager struct {
	WorkspaceDir string
	SnapshotRoot string
	Options      WorkspaceSnapshotOptions
	mu           sync.Mutex
	pendingMu    sync.Mutex
	pending      map[string]<-chan error
	lastManifest *snapshotManifest
	seq          uint64
}

func NewWorkspaceSnapshotManager(workspaceDir, snapshotRoot string) *WorkspaceSnapshotManager {
	return NewWorkspaceSnapshotManagerWithOptions(workspaceDir, snapshotRoot, WorkspaceSnapshotOptions{
		Mode: SnapshotModeDelta,
	})
}

func NewWorkspaceSnapshotManagerWithOptions(workspaceDir, snapshotRoot string, opts WorkspaceSnapshotOptions) *WorkspaceSnapshotManager {
	if opts.Mode == "" {
		opts.Mode = SnapshotModeDelta
	}
	return &WorkspaceSnapshotManager{
		WorkspaceDir: filepath.Clean(workspaceDir),
		SnapshotRoot: filepath.Clean(snapshotRoot),
		Options:      opts,
	}
}

func (m *WorkspaceSnapshotManager) Capture(ctx context.Context, req SnapshotRequest) (SnapshotResult, error) {
	id := m.nextSnapshotID(req)
	return m.captureWithID(ctx, req, id)
}

// CaptureAsync starts a snapshot in the background. It is intended for non-preimage
// backups where callers can tolerate waiting on Done before restore.
func (m *WorkspaceSnapshotManager) CaptureAsync(ctx context.Context, req SnapshotRequest) (AsyncSnapshot, error) {
	if err := m.validate(); err != nil {
		return AsyncSnapshot{}, err
	}
	id := m.nextSnapshotID(req)
	done := make(chan error, 1)
	m.registerPendingSnapshot(id, done)
	go func() {
		_, err := m.captureWithID(ctx, req, id)
		done <- err
		close(done)
		m.clearPendingSnapshot(id, done)
	}()
	return AsyncSnapshot{
		Result: SnapshotResult{ID: id, Method: string(m.snapshotMode()) + "_async"},
		Done:   done,
	}, nil
}

func (m *WorkspaceSnapshotManager) captureWithID(ctx context.Context, req SnapshotRequest, id string) (SnapshotResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validate(); err != nil {
		return SnapshotResult{}, err
	}
	if ctx.Err() != nil {
		return SnapshotResult{}, ctx.Err()
	}
	if err := os.MkdirAll(m.SnapshotRoot, 0o755); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: mkdir root: %w", err)
	}
	if m.snapshotMode() == SnapshotModeFullCopy {
		return m.captureFullCopy(ctx, id)
	}
	return m.captureDelta(ctx, id)
}

func (m *WorkspaceSnapshotManager) validate() error {
	if strings.TrimSpace(m.WorkspaceDir) == "" || strings.TrimSpace(m.SnapshotRoot) == "" {
		return fmt.Errorf("workspace snapshot: workspace and snapshot root are required")
	}
	return nil
}

func (m *WorkspaceSnapshotManager) snapshotMode() SnapshotMode {
	switch m.Options.Mode {
	case SnapshotModeFullCopy:
		return SnapshotModeFullCopy
	default:
		return SnapshotModeDelta
	}
}

func (m *WorkspaceSnapshotManager) captureFullCopy(ctx context.Context, id string) (SnapshotResult, error) {
	if ctx.Err() != nil {
		return SnapshotResult{}, ctx.Err()
	}
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

func (m *WorkspaceSnapshotManager) Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error) {
	if strings.TrimSpace(req.SnapshotID) == "" {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: snapshot id is required")
	}
	if err := m.waitPendingSnapshot(ctx, req.SnapshotID); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: wait for pending snapshot %s: %w", req.SnapshotID, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	src := filepath.Join(m.SnapshotRoot, req.SnapshotID)
	if _, err := os.Stat(src); err != nil {
		return RestoreResult{}, fmt.Errorf("workspace snapshot restore: stat %s: %w", src, err)
	}
	if isDeltaSnapshotDir(src) {
		manifest, err := readSnapshotManifest(src)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("workspace snapshot restore: read manifest %s: %w", req.SnapshotID, err)
		}
		if err := m.restoreDelta(ctx, manifest); err != nil {
			return RestoreResult{}, fmt.Errorf("workspace snapshot restore: restore %s: %w", req.SnapshotID, err)
		}
		return RestoreResult{SnapshotID: req.SnapshotID, Method: manifest.Method}, nil
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

func (m *WorkspaceSnapshotManager) registerPendingSnapshot(id string, done <-chan error) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pending == nil {
		m.pending = map[string]<-chan error{}
	}
	m.pending[id] = done
}

func (m *WorkspaceSnapshotManager) clearPendingSnapshot(id string, done <-chan error) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pending[id] == done {
		delete(m.pending, id)
	}
}

func (m *WorkspaceSnapshotManager) waitPendingSnapshot(ctx context.Context, id string) error {
	m.pendingMu.Lock()
	done := m.pending[id]
	m.pendingMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case err := <-done:
		m.clearPendingSnapshot(id, done)
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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

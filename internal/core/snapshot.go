package core

import "context"

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

package core

import (
	"context"

	"github.com/tripledoublev/v100/internal/providers"
)

// Checkpoint represents a snapshot of the agent state.
type Checkpoint struct {
	Messages   []providers.Message
	StepCount  int
	SnapshotID string
}

// Checkpoint captures the current state of the loop.
func (l *Loop) Checkpoint() Checkpoint {
	cp, _ := l.CheckpointWithContext(context.Background())
	return cp
}

// CheckpointWithContext captures the current logical and filesystem state of the loop.
func (l *Loop) CheckpointWithContext(ctx context.Context) (Checkpoint, error) {
	msgs := make([]providers.Message, len(l.Messages))
	copy(msgs, l.Messages)
	cp := Checkpoint{
		Messages:  msgs,
		StepCount: l.stepCount,
	}
	if l.Snapshots != nil {
		snap, err := l.snapshotManager().Capture(ctx, SnapshotRequest{
			RunID:  l.Run.ID,
			Reason: "checkpoint",
		})
		if err != nil {
			return Checkpoint{}, err
		}
		cp.SnapshotID = snap.ID
		_, err = l.emit(EventSandboxSnapshot, "", SandboxSnapshotPayload{
			SnapshotID: snap.ID,
			Method:     snap.Method,
			Reason:     "checkpoint",
		})
		if err != nil {
			return Checkpoint{}, err
		}
	}
	return cp, nil
}

// Restore resets the loop state to a previous checkpoint.
func (l *Loop) Restore(cp Checkpoint) {
	_ = l.RestoreWithContext(context.Background(), cp)
}

// RestoreWithContext resets the loop state and workspace to a previous checkpoint.
func (l *Loop) RestoreWithContext(ctx context.Context, cp Checkpoint) error {
	if l.Snapshots != nil && cp.SnapshotID != "" {
		res, err := l.snapshotManager().Restore(ctx, RestoreRequest{
			RunID:      l.Run.ID,
			SnapshotID: cp.SnapshotID,
			Reason:     "checkpoint",
		})
		if err != nil {
			return err
		}
		if _, err := l.emit(EventSandboxRestore, "", SandboxRestorePayload{
			SnapshotID: res.SnapshotID,
			Method:     res.Method,
			Reason:     "checkpoint",
		}); err != nil {
			return err
		}
	}
	l.Messages = make([]providers.Message, len(cp.Messages))
	copy(l.Messages, cp.Messages)
	l.stepCount = cp.StepCount
	return nil
}

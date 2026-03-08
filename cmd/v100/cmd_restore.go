package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func restoreCmd(_ *string) *cobra.Command {
	var listFlag bool

	cmd := &cobra.Command{
		Use:   "restore <run_id> [checkpoint_id]",
		Short: "Restore a sandboxed run workspace to a persisted checkpoint",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runIDArg := args[0]
			runDir, err := findRunDir(runIDArg)
			if err != nil {
				return err
			}
			runID := filepath.Base(runDir)

			if listFlag {
				return listCheckpoints(runDir)
			}

			checkpoint, err := resolveCheckpointForRestore(runDir, args[1:])
			if err != nil {
				return err
			}
			if checkpoint.SnapshotID == "" {
				return fmt.Errorf("checkpoint %q has no snapshot id", checkpoint.ID)
			}

			sandboxWorkspace := filepath.Join(filepath.Dir(runDir), runID, "workspace")
			snapshots := core.NewWorkspaceSnapshotManager(sandboxWorkspace, filepath.Join(filepath.Dir(sandboxWorkspace), "snapshots"))
			restoreResult, err := snapshots.Restore(context.Background(), core.RestoreRequest{
				RunID:      runID,
				SnapshotID: checkpoint.SnapshotID,
				Reason:     "manual_restore",
			})
			if err != nil {
				return err
			}

			tracePath := filepath.Join(runDir, "trace.jsonl")
			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return err
			}
			defer func() { _ = trace.Close() }()

			payload, err := json.Marshal(core.SandboxRestorePayload{
				SnapshotID: restoreResult.SnapshotID,
				Method:     restoreResult.Method,
				Reason:     "manual_restore",
			})
			if err != nil {
				return err
			}
			if err := trace.Write(core.Event{
				TS:      time.Now().UTC(),
				RunID:   runID,
				EventID: fmt.Sprintf("restore-%d", time.Now().UTC().UnixNano()),
				Type:    core.EventSandboxRestore,
				Payload: payload,
			}); err != nil {
				return err
			}

			fmt.Println(ui.Info(fmt.Sprintf("restored checkpoint %s into %s", checkpoint.ID, sandboxWorkspace)))
			fmt.Println(ui.Info(fmt.Sprintf("resume with: v100 resume %s", runID)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&listFlag, "list", false, "list available checkpoints for the run")
	return cmd
}

func resolveCheckpointForRestore(runDir string, args []string) (core.Checkpoint, error) {
	if len(args) > 0 {
		return core.ReadCheckpoint(runDir, args[0])
	}
	return core.LatestCheckpoint(runDir)
}

func listCheckpoints(runDir string) error {
	checkpoints, err := core.ListCheckpoints(runDir)
	if err != nil {
		return err
	}
	if len(checkpoints) == 0 {
		fmt.Println(ui.Warn("no checkpoints found"))
		return nil
	}
	for _, cp := range checkpoints {
		fmt.Printf("%s  snapshot=%s  step_count=%d  messages=%d\n",
			cp.ID,
			cp.SnapshotID,
			cp.StepCount,
			len(cp.Messages),
		)
	}
	return nil
}

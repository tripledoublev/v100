package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func steerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "steer <run_id> <message>",
		Short: "Inject an asynchronous steering message into an active run trace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := strings.TrimSpace(args[0])
			message := strings.TrimSpace(args[1])
			if runID == "" {
				return errors.New("run_id is required")
			}
			if message == "" {
				return errors.New("steering message cannot be empty")
			}
			runDir, err := findRunDir(runID)
			if err != nil {
				return fmt.Errorf("find run: %w", err)
			}
			tracePath := filepath.Join(runDir, "trace.jsonl")
			trace, err := core.OpenTrace(tracePath)
			if err != nil {
				return fmt.Errorf("open trace: %w", err)
			}
			defer func() { _ = trace.Close() }()
			payload, err := json.Marshal(core.UserMsgPayload{
				Content: message,
				Source:  "steer",
			})
			if err != nil {
				return fmt.Errorf("marshal steering payload: %w", err)
			}
			now := time.Now().UTC()
			if err := trace.Write(core.Event{
				TS:      now,
				RunID:   filepath.Base(runDir),
				EventID: fmt.Sprintf("steer-%d", now.UnixNano()),
				Type:    core.EventUserMsg,
				Payload: payload,
			}); err != nil {
				return fmt.Errorf("write steering event: %w", err)
			}
			fmt.Printf("%s  steering message appended to %s\n", ui.OK("steer"), tracePath)
			return nil
		},
	}
}

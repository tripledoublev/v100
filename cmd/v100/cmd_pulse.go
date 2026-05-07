package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
)

func pulseCmd() *cobra.Command {
	var runDir string
	cmd := &cobra.Command{
		Use:   "pulse [run_id_or_trace_file]",
		Short: "Show a one-line summary of an active run",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) > 0 {
				target = args[0]
			}
			line, err := buildPulseLine(target, runDir, time.Now())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "runs directory to scan when no run id is provided (default: ./runs)")
	return cmd
}

func buildPulseLine(target, runDir string, now time.Time) (string, error) {
	runID, events, err := loadPulseEvents(target, runDir)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return fmt.Sprintf("%s idle no trace events yet", runID), nil
	}
	last := events[len(events)-1]
	state := pulseState(events)
	activity := pulseActivity(last)
	if activity == "" {
		activity = string(last.Type)
	}
	return fmt.Sprintf("%s %s %s %s", runID, state, humanDuration(now.Sub(last.TS)), activity), nil
}

func loadPulseEvents(target, runDir string) (string, []core.Event, error) {
	target = strings.TrimSpace(target)
	if target != "" {
		runDir, err := findRunDir(target)
		if err != nil {
			return "", nil, err
		}
		events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
		return filepath.Base(runDir), events, err
	}
	if strings.TrimSpace(runDir) == "" {
		runDir = "runs"
	}
	dir, err := latestActiveRunDir(runDir)
	if err != nil {
		return "", nil, err
	}
	events, err := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
	return filepath.Base(dir), events, err
}

func latestActiveRunDir(runDir string) (string, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return "", err
	}
	type candidate struct {
		dir     string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(runDir, entry.Name())
		if !isPrimaryRunDir(dir, entry.Name()) {
			continue
		}
		events, err := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
		if err != nil || len(events) == 0 || traceHasRunEnd(events) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{dir: dir, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no active runs found in %s", runDir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates[0].dir, nil
}

func traceHasRunEnd(events []core.Event) bool {
	for _, ev := range events {
		if ev.Type == core.EventRunEnd {
			return true
		}
	}
	return false
}

func pulseState(events []core.Event) string {
	if traceHasRunEnd(events) {
		return "ended"
	}
	return "active"
}

func pulseActivity(ev core.Event) string {
	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		parts := []string{"started"}
		if strings.TrimSpace(p.Provider) != "" {
			parts = append(parts, p.Provider)
		}
		if strings.TrimSpace(p.Model) != "" {
			parts = append(parts, p.Model)
		}
		return strings.Join(parts, " ")
	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Source == "system" {
			return "system prompt queued"
		}
		return "user input queued"
	case core.EventModelCall:
		return "calling model"
	case core.EventModelToken:
		return "streaming model response"
	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if len(p.ToolCalls) > 0 {
			return fmt.Sprintf("model requested %d tool call(s)", len(p.ToolCalls))
		}
		return "model response received"
	case core.EventToolCall:
		var p core.ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if strings.TrimSpace(p.Name) == "" {
			return "running tool"
		}
		return "running tool " + p.Name
	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		status := "completed"
		if !p.OK {
			status = "failed"
		}
		if strings.TrimSpace(p.Name) == "" {
			return "tool " + status
		}
		return fmt.Sprintf("tool %s %s", p.Name, status)
	case core.EventCompress:
		return "compressing context"
	case core.EventHookIntervention:
		var p core.HookInterventionPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if strings.TrimSpace(p.Reason) != "" {
			return "hook intervention " + p.Reason
		}
		return "hook intervention"
	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if strings.TrimSpace(p.Error) != "" {
			return "error " + compactPulseText(p.Error, 80)
		}
		return "error"
	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if strings.TrimSpace(p.Reason) != "" {
			return "ended " + p.Reason
		}
		return "ended"
	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.StepNumber > 0 {
			return fmt.Sprintf("finished step %d", p.StepNumber)
		}
		return "finished step"
	default:
		return string(ev.Type)
	}
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "just-now"
	case d < time.Minute:
		return fmt.Sprintf("%ds-ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm-ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh-ago", int(d.Hours()))
	}
}

func compactPulseText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	if limit <= 1 {
		return s[:limit]
	}
	return s[:limit-1] + "…"
}

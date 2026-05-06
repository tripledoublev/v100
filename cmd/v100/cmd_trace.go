package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func traceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Import and export cross-harness trace formats",
	}
	cmd.AddCommand(traceExportCmd(), traceImportCmd())
	return cmd
}

func traceExportCmd() *cobra.Command {
	var format string
	var output string
	cmd := &cobra.Command{
		Use:   "export <run_id>",
		Short: "Export a v100 run trace as inspect or metr JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir, err := findRunDir(args[0])
			if err != nil {
				return err
			}
			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			format = normalizeTraceFormat(format)
			data, err := marshalHarnessTrace(format, filepath.Base(runDir), events)
			if err != nil {
				return err
			}
			if output == "" {
				output = filepath.Join(runDir, fmt.Sprintf("trace.%s.json", format))
			}
			if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(output, data, 0o644); err != nil {
				return err
			}
			fmt.Println(ui.Info("wrote " + format + " trace: " + output))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "inspect", "export format: inspect or metr")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output path")
	return cmd
}

func traceImportCmd() *cobra.Command {
	var format string
	var runID string
	cmd := &cobra.Command{
		Use:   "import <trace_file>",
		Short: "Import an inspect or metr trace into runs/<id>/trace.jsonl",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := readHarnessTrace(args[0], format)
			if err != nil {
				return err
			}
			if runID == "" {
				runID = fmt.Sprintf("import-%s-%d", normalizeTraceFormat(format), time.Now().UTC().Unix())
			}
			runDir := filepath.Join("runs", runID)
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				return err
			}
			trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			defer func() { _ = trace.Close() }()
			for i, ev := range events {
				if ev.RunID == "" {
					ev.RunID = runID
				}
				if ev.EventID == "" {
					ev.EventID = fmt.Sprintf("import-%d", i+1)
				}
				if ev.TS.IsZero() {
					ev.TS = time.Now().UTC()
				}
				if err := trace.Write(ev); err != nil {
					return err
				}
			}
			fmt.Println(ui.Info(fmt.Sprintf("imported %d events into %s", len(events), runDir)))
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto, inspect, or metr")
	cmd.Flags().StringVar(&runID, "run-id", "", "run id for imported trace")
	return cmd
}

func normalizeTraceFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "auto":
		return "inspect"
	case "inspect", "aisi":
		return "inspect"
	case "metr":
		return "metr"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func marshalHarnessTrace(format, runID string, events []core.Event) ([]byte, error) {
	switch normalizeTraceFormat(format) {
	case "inspect":
		return json.MarshalIndent(map[string]any{
			"format":  "inspect",
			"version": "v100.inspect.v1",
			"run_id":  runID,
			"events":  events,
		}, "", "  ")
	case "metr":
		items := make([]map[string]any, len(events))
		for i, ev := range events {
			items[i] = map[string]any{
				"kind":      ev.Type,
				"timestamp": ev.TS,
				"step":      ev.StepID,
				"event_id":  ev.EventID,
				"data":      json.RawMessage(ev.Payload),
			}
		}
		return json.MarshalIndent(map[string]any{
			"format":     "metr",
			"schema":     "v100.metr.trace.v1",
			"run_id":     runID,
			"trajectory": items,
		}, "", "  ")
	default:
		return nil, fmt.Errorf("unsupported trace format %q", format)
	}
}

func readHarnessTrace(path, format string) ([]core.Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(format) == "" || strings.EqualFold(format, "auto") {
		format = detectHarnessTraceFormat(data)
	}
	if strings.HasSuffix(path, ".jsonl") {
		if events, err := readHarnessJSONL(path); err == nil && len(events) > 0 {
			return events, nil
		}
	}
	return parseHarnessTraceJSON(data, normalizeTraceFormat(format))
}

func detectHarnessTraceFormat(data []byte) string {
	text := string(data)
	if strings.Contains(text, `"trajectory"`) || strings.Contains(text, `"metr"`) {
		return "metr"
	}
	return "inspect"
}

func readHarnessJSONL(path string) ([]core.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var events []core.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		ev, err := parseHarnessEvent(json.RawMessage(line))
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

func parseHarnessTraceJSON(data []byte, format string) ([]core.Event, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	root, ok := raw.(map[string]any)
	if !ok {
		if arr, ok := raw.([]any); ok {
			return parseHarnessEventArray(arr)
		}
		return nil, fmt.Errorf("trace root must be an object or array")
	}
	var arr []any
	switch format {
	case "metr":
		arr, _ = root["trajectory"].([]any)
		if len(arr) == 0 {
			arr, _ = root["events"].([]any)
		}
	default:
		arr, _ = root["events"].([]any)
		if len(arr) == 0 {
			arr, _ = root["samples"].([]any)
		}
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("no events found in %s trace", format)
	}
	return parseHarnessEventArray(arr)
}

func parseHarnessEventArray(items []any) ([]core.Event, error) {
	events := make([]core.Event, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		ev, err := parseHarnessEvent(raw)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func parseHarnessEvent(raw json.RawMessage) (core.Event, error) {
	var ev core.Event
	if json.Unmarshal(raw, &ev) == nil && ev.Type != "" {
		return ev, nil
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return core.Event{}, err
	}
	ev.Type = core.EventType(firstJSONText(generic, "type", "event_type", "kind", "name"))
	ev.RunID = firstJSONText(generic, "run_id", "runId", "sample_id")
	ev.StepID = firstJSONText(generic, "step_id", "stepId", "step")
	ev.EventID = firstJSONText(generic, "event_id", "eventId", "id")
	if ts := firstJSONText(generic, "ts", "timestamp", "time"); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ev.TS = parsed
		}
	}
	for _, key := range []string{"payload", "data", "content", "message"} {
		if payload, ok := generic[key]; ok {
			ev.Payload = payload
			break
		}
	}
	if ev.Payload == nil {
		ev.Payload = json.RawMessage(`{}`)
	}
	if ev.Type == "" {
		ev.Type = core.EventModelResp
	}
	return ev, nil
}

func firstJSONText(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var n float64
		if json.Unmarshal(raw, &n) == nil {
			return fmt.Sprintf("%.0f", n)
		}
	}
	return ""
}

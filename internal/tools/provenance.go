package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/provenance"
)

type provenanceLookupTool struct{}

func ProvenanceLookup() Tool { return &provenanceLookupTool{} }

func (t *provenanceLookupTool) Name() string { return "provenance_lookup" }
func (t *provenanceLookupTool) Description() string {
	return "Show the trace event and model reasoning that last touched a file or line in a prior run."
}
func (t *provenanceLookupTool) DangerLevel() DangerLevel { return Safe }
func (t *provenanceLookupTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *provenanceLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative or absolute file path to inspect."},
			"line": {"type": "integer", "description": "Optional 1-based line number to inspect."},
			"run_id": {"type": "string", "description": "Run ID, run directory, or trace.jsonl path. Defaults to the current run when available."},
			"runs_dir": {"type": "string", "description": "Directory containing run folders when run_id is provided. Default: runs."},
			"max_results": {"type": "integer", "description": "Maximum provenance records to return.", "default": 5}
		}
	}`)
}

func (t *provenanceLookupTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"matches": {"type": "array", "items": {"type": "object"}},
			"trace_path": {"type": "string"}
		}
	}`)
}

func (t *provenanceLookupTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Path       string `json:"path"`
		Line       int    `json:"line"`
		RunID      string `json:"run_id"`
		RunsDir    string `json:"runs_dir"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if strings.TrimSpace(a.Path) == "" {
		return failResult(start, "path is required"), nil
	}
	if a.MaxResults <= 0 {
		a.MaxResults = 5
	}

	tracePath, err := resolveProvenanceTrace(call, a.RunID, a.RunsDir)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	events, err := provenance.ReadAll(tracePath)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	matches := provenance.Find(provenance.Build(events), a.Path, a.Line)
	if len(matches) > a.MaxResults {
		matches = matches[:a.MaxResults]
	}
	payload := map[string]any{
		"trace_path": tracePath,
		"matches":    matches,
	}
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	return ToolResult{OK: true, Output: string(out), DurationMS: time.Since(start).Milliseconds()}, nil
}

func resolveProvenanceTrace(call ToolCallContext, runID, runsDir string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runsDir == "" {
		runsDir = "runs"
	}
	if call.WorkspaceDir != "" && !filepath.IsAbs(runsDir) {
		runsDir = filepath.Join(call.WorkspaceDir, runsDir)
	}
	if runID == "" {
		if call.RunID == "" {
			return "", fmt.Errorf("run_id is required when current run id is unavailable")
		}
		runID = call.RunID
	}
	runID = strings.TrimSuffix(runID, "/trace.jsonl")
	runID = strings.TrimSuffix(runID, string(filepath.Separator)+"trace.jsonl")
	if info, err := os.Stat(runID); err == nil {
		if info.IsDir() {
			return filepath.Join(runID, "trace.jsonl"), nil
		}
		return runID, nil
	}
	candidate := filepath.Join(runsDir, runID, "trace.jsonl")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	var nested string
	_ = filepath.WalkDir(runsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || nested != "" || !d.IsDir() {
			return nil
		}
		if filepath.Base(path) != runID {
			return nil
		}
		trace := filepath.Join(path, "trace.jsonl")
		if _, err := os.Stat(trace); err == nil {
			nested = trace
			return fs.SkipAll
		}
		return nil
	})
	if nested != "" {
		return nested, nil
	}
	return "", fmt.Errorf("run %q not found under %s", runID, runsDir)
}

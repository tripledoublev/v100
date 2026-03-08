package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type orchestrateTool struct {
	runFn      AgentRunFn
	listAgents func() []string
}

// NewOrchestrate creates a coordination tool for fanout/pipeline dispatch.
func NewOrchestrate(runFn AgentRunFn, listAgents func() []string) Tool {
	return &orchestrateTool{runFn: runFn, listAgents: listAgents}
}

func (t *orchestrateTool) Name() string { return "orchestrate" }
func (t *orchestrateTool) Description() string {
	return "Coordinate multiple dispatched specialist agents."
}
func (t *orchestrateTool) DangerLevel() DangerLevel { return Dangerous }
func (t *orchestrateTool) Effects() ToolEffects {
	return ToolEffects{
		MutatesWorkspace:   true,
		MutatesRunState:    true,
		NeedsNetwork:       true,
		ExternalSideEffect: true,
	}
}
func (t *orchestrateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"required":["pattern","tasks"],
		"properties":{
			"pattern":{"type":"string","enum":["fanout","pipeline"]},
			"tasks":{"type":"array","items":{
				"type":"object",
				"required":["agent","task"],
				"properties":{
					"agent":{"type":"string"},
					"task":{"type":"string"},
					"model":{"type":"string"},
					"max_steps":{"type":"integer"}
				}
			}},
			"max_parallel":{"type":"integer","description":"For fanout only; default 4."}
		}
	}`)
}
func (t *orchestrateTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"ok":{"type":"boolean"},
			"result":{"type":"string"}
		}
	}`)
}

func (t *orchestrateTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a struct {
		Pattern     string `json:"pattern"`
		MaxParallel int    `json:"max_parallel"`
		Tasks       []struct {
			Agent    string `json:"agent"`
			Task     string `json:"task"`
			Model    string `json:"model"`
			MaxSteps int    `json:"max_steps"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if t.runFn == nil {
		return failResult(start, "orchestrate not wired"), nil
	}
	if a.Pattern != "fanout" && a.Pattern != "pipeline" {
		return failResult(start, "pattern must be fanout or pipeline"), nil
	}
	if len(a.Tasks) == 0 {
		return failResult(start, "tasks is required"), nil
	}
	if a.MaxParallel <= 0 {
		a.MaxParallel = 4
	}

	results := make([]AgentRunResult, len(a.Tasks))
	ok := true

	if a.Pattern == "pipeline" {
		for i, task := range a.Tasks {
			res := t.runFn(ctx, AgentRunParams{
				CallID:       fmt.Sprintf("%s-p%d", call.CallID, i+1),
				RunID:        call.RunID,
				StepID:       call.StepID,
				Agent:        strings.TrimSpace(task.Agent),
				Pattern:      a.Pattern,
				Task:         task.Task,
				Model:        task.Model,
				MaxSteps:     task.MaxSteps,
				WorkspaceDir: call.WorkspaceDir,
			})
			results[i] = res
			if !res.OK {
				ok = false
			}
			_ = appendBlackboardDispatch(call.RunID, a.Pattern, task.Agent, task.Task, res)
		}
	} else {
		sem := make(chan struct{}, a.MaxParallel)
		var wg sync.WaitGroup
		for i, task := range a.Tasks {
			i, task := i, task
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res := t.runFn(ctx, AgentRunParams{
					CallID:       fmt.Sprintf("%s-f%d", call.CallID, i+1),
					RunID:        call.RunID,
					StepID:       call.StepID,
					Agent:        strings.TrimSpace(task.Agent),
					Pattern:      a.Pattern,
					Task:         task.Task,
					Model:        task.Model,
					MaxSteps:     task.MaxSteps,
					WorkspaceDir: call.WorkspaceDir,
				})
				results[i] = res
				_ = appendBlackboardDispatch(call.RunID, a.Pattern, task.Agent, task.Task, res)
			}()
		}
		wg.Wait()
		for _, r := range results {
			if !r.OK {
				ok = false
			}
		}
	}

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "[orchestrate pattern=%s tasks=%d]\n", a.Pattern, len(a.Tasks))
	for i, task := range a.Tasks {
		res := results[i]
		out := strings.TrimSpace(res.Result)
		if len(out) > 180 {
			out = out[:180] + "…"
		}
		if out == "" {
			out = "(no output)"
		}
		status := "ok"
		if !res.OK {
			status = "fail"
		}
		_, _ = fmt.Fprintf(&b, "%d. [%s] %s  steps=%d tok=%d cost=$%.4f\n   %s\n",
			i+1, status, task.Agent, res.UsedSteps, res.UsedTokens, res.CostUSD, out)
	}
	jsonResults := make([]map[string]any, 0, len(a.Tasks))
	for i, task := range a.Tasks {
		r := results[i]
		jsonResults = append(jsonResults, map[string]any{
			"agent":       task.Agent,
			"ok":          r.OK,
			"used_steps":  r.UsedSteps,
			"used_tokens": r.UsedTokens,
			"cost_usd":    r.CostUSD,
			"result":      r.Result,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"ok":      ok,
		"pattern": a.Pattern,
		"results": jsonResults,
	})
	b.WriteString("\njson=" + string(payload) + "\n")

	return ToolResult{
		OK:         ok,
		Output:     b.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func appendBlackboardDispatch(runID, pattern, agent, task string, res AgentRunResult) error {
	path := blackboardPath(runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st := "ok"
	if !res.OK {
		st = "fail"
	}
	out := strings.TrimSpace(res.Result)
	if len(out) > 240 {
		out = out[:240] + "…"
	}
	_, err = fmt.Fprintf(f,
		"\n## Dispatch (%s)\n- agent: %s\n- status: %s\n- steps: %d\n- tokens: %d\n- cost: $%.4f\n- task: %s\n- result: %s\n",
		pattern, agent, st, res.UsedSteps, res.UsedTokens, res.CostUSD, strings.TrimSpace(task), out)
	return err
}

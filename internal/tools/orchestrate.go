package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	b.WriteString(fmt.Sprintf("[orchestrate pattern=%s tasks=%d]\n", a.Pattern, len(a.Tasks)))
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
		b.WriteString(fmt.Sprintf("%d. [%s] %s  steps=%d tok=%d cost=$%.4f\n   %s\n",
			i+1, status, task.Agent, res.UsedSteps, res.UsedTokens, res.CostUSD, out))
	}

	return ToolResult{
		OK:         ok,
		Output:     b.String(),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

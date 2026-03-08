package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type dispatchTool struct {
	runFn      AgentRunFn
	listAgents func() []string
}

// NewDispatch creates a role-based dispatch tool.
func NewDispatch(runFn AgentRunFn, listAgents func() []string) Tool {
	return &dispatchTool{runFn: runFn, listAgents: listAgents}
}

func (t *dispatchTool) Name() string { return "dispatch" }
func (t *dispatchTool) Description() string {
	return "Dispatch a task to a named specialist agent role."
}
func (t *dispatchTool) DangerLevel() DangerLevel { return Dangerous }
func (t *dispatchTool) Effects() ToolEffects {
	return ToolEffects{
		MutatesWorkspace:   true,
		MutatesRunState:    true,
		NeedsNetwork:       true,
		ExternalSideEffect: true,
	}
}

func (t *dispatchTool) InputSchema() json.RawMessage {
	agents := []string{}
	if t.listAgents != nil {
		agents = append(agents, t.listAgents()...)
		sort.Strings(agents)
	}
	agentEnum := ""
	if len(agents) > 0 {
		quoted := make([]string, 0, len(agents))
		for _, a := range agents {
			quoted = append(quoted, fmt.Sprintf("%q", a))
		}
		agentEnum = fmt.Sprintf(`, "enum": [%s]`, strings.Join(quoted, ", "))
	}
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"required": ["agent", "task"],
		"properties": {
			"agent":     {"type": "string"%s, "description": "Named agent role from config [agents.<name>]."},
			"task":      {"type": "string", "description": "Task for the dispatched specialist."},
			"model":     {"type": "string", "description": "Optional model override for this dispatch."},
			"max_steps": {"type": "integer", "description": "Optional step cap override for this dispatch."}
		}
	}`, agentEnum))
}

func (t *dispatchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"result": {"type": "string"}
		}
	}`)
}

func (t *dispatchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Agent    string `json:"agent"`
		Task     string `json:"task"`
		Model    string `json:"model"`
		MaxSteps int    `json:"max_steps"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if strings.TrimSpace(a.Agent) == "" {
		return failResult(start, "agent is required"), nil
	}
	if strings.TrimSpace(a.Task) == "" {
		return failResult(start, "task is required"), nil
	}

	if t.runFn == nil {
		return failResult(start, "dispatch tool not wired: no run function"), nil
	}

	res := t.runFn(ctx, AgentRunParams{
		CallID:       call.CallID,
		RunID:        call.RunID,
		StepID:       call.StepID,
		Agent:        a.Agent,
		Task:         a.Task,
		Model:        a.Model,
		MaxSteps:     a.MaxSteps,
		WorkspaceDir: call.WorkspaceDir,
	})

	output := res.Result
	if output == "" && !res.OK {
		output = "(dispatch failed with no result)"
	}
	if output == "" {
		output = "(dispatch produced no output)"
	}

	payload, _ := json.Marshal(map[string]any{
		"ok":          res.OK,
		"agent":       a.Agent,
		"used_steps":  res.UsedSteps,
		"used_tokens": res.UsedTokens,
		"cost_usd":    res.CostUSD,
		"result":      output,
	})
	summary := fmt.Sprintf("[dispatch %s done: steps=%d tokens=%d cost=$%.4f]\njson=%s\n\n%s",
		a.Agent, res.UsedSteps, res.UsedTokens, res.CostUSD, string(payload), output)

	return ToolResult{
		OK:         res.OK,
		Output:     summary,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

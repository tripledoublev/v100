package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AgentRunFn runs a sub-agent with the given parameters and returns the result.
// It is injected by the wiring layer to avoid import cycles between tools and core.
type AgentRunFn func(ctx context.Context, params AgentRunParams) AgentRunResult

// AgentRunParams describes the sub-agent invocation.
type AgentRunParams struct {
	CallID       string
	RunID        string
	StepID       string
	Agent        string
	Pattern      string
	Task         string
	Model        string
	Tools        []string
	MaxSteps     int
	WorkspaceDir string
}

// AgentRunResult holds the sub-agent's outcome.
type AgentRunResult struct {
	OK         bool
	Result     string
	UsedSteps  int
	UsedTokens int
	CostUSD    float64
}

type agentTool struct {
	runFn AgentRunFn
}

// NewAgent creates a new agent tool instance.
// The runFn callback is invoked at Exec time to run the child loop.
func NewAgent(runFn AgentRunFn) Tool {
	return &agentTool{runFn: runFn}
}

func (t *agentTool) Name() string             { return "agent" }
func (t *agentTool) Description() string      { return "Spawn a sub-agent to handle a focused task." }
func (t *agentTool) DangerLevel() DangerLevel { return Dangerous }

func (t *agentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":      {"type": "string", "description": "Prompt/task for the sub-agent."},
			"model":     {"type": "string", "description": "Model override (e.g. gpt-5.3-codex). Empty = reuse parent model."},
			"tools":     {"type": "array", "items": {"type": "string"}, "description": "Tool subset for the sub-agent. Default = all parent tools except agent."},
			"max_steps": {"type": "integer", "description": "Step limit for the sub-agent (default 10)."}
		}
	}`)
}

func (t *agentTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"result": {"type": "string"}
		}
	}`)
}

func (t *agentTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Task     string   `json:"task"`
		Model    string   `json:"model"`
		Tools    []string `json:"tools"`
		MaxSteps int      `json:"max_steps"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if a.Task == "" {
		return failResult(start, "task is required"), nil
	}
	if a.MaxSteps <= 0 {
		a.MaxSteps = 10
	}

	if t.runFn == nil {
		return failResult(start, "agent tool not wired: no run function"), nil
	}

	res := t.runFn(ctx, AgentRunParams{
		CallID:       call.CallID,
		RunID:        call.RunID,
		StepID:       call.StepID,
		Task:         a.Task,
		Model:        a.Model,
		Tools:        a.Tools,
		MaxSteps:     a.MaxSteps,
		WorkspaceDir: call.WorkspaceDir,
	})

	output := res.Result
	if output == "" && !res.OK {
		output = "(sub-agent failed with no result)"
	}
	if output == "" {
		output = "(sub-agent produced no output)"
	}

	summary := fmt.Sprintf("[agent done: steps=%d tokens=%d cost=$%.4f]\n\n%s",
		res.UsedSteps, res.UsedTokens, res.CostUSD, output)

	return ToolResult{
		OK:         res.OK,
		Output:     summary,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

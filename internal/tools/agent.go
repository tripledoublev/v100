package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AgentRunFn runs a sub-agent with the given parameters and returns the result.
// It is injected by the wiring layer to avoid import cycles between tools and core.
type AgentRunFn func(ctx context.Context, params AgentRunParams) AgentRunResult

// AgentRunParams describes the sub-agent invocation.
type AgentRunParams struct {
	CallID            string
	RunID             string
	StepID            string
	Agent             string
	Pattern           string
	Task              string
	Provider          string
	Model             string
	Tools             []string
	MaxSteps          int
	HandoffSchemaName string
	HandoffSchema     json.RawMessage
	WorkspaceDir      string
	StateDir          string
}

// AgentRunResult holds the sub-agent's outcome.
type AgentRunResult struct {
	OK          bool
	AgentRunID  string
	Result      string
	Structured  json.RawMessage
	Diagnostics []string
	UsedSteps   int
	UsedTokens  int
	CostUSD     float64
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
func (t *agentTool) Effects() ToolEffects {
	return ToolEffects{
		MutatesWorkspace:   true,
		MutatesRunState:    true,
		NeedsNetwork:       true,
		ExternalSideEffect: true,
	}
}

func (t *agentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":      {"type": "string", "description": "Prompt/task for the sub-agent."},
			"provider":  {"type": "string", "description": "Provider override (e.g. glm, minimax, gemini, codex). Empty = reuse parent provider."},
			"model":     {"type": "string", "description": "Model override for the selected provider. Empty = provider default."},
			"tools":     {"type": "array", "items": {"type": "string"}, "description": "Tool subset for the sub-agent. Default = all parent tools except agent."},
			"max_steps": {"type": "integer", "description": "Step limit for the sub-agent (default 10)."},
			"handoff_schema_name": {"type": "string", "description": "Named structured handoff schema to require, e.g. standard."},
			"handoff_schema": {"type": "object", "description": "Custom JSON Schema subset for the sub-agent final result."}
		}
	}`)
}

func (t *agentTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok": {"type": "boolean"},
			"agent_run_id": {"type": "string"},
			"used_steps": {"type": "integer"},
			"used_tokens": {"type": "integer"},
			"cost_usd": {"type": "number"},
			"result": {"type": "string"},
			"handoff": {"type": "object"},
			"diagnostics": {"type": "array", "items": {"type": "string"}}
		}
	}`)
}

func (t *agentTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Task              string          `json:"task"`
		Provider          string          `json:"provider"`
		Model             string          `json:"model"`
		Tools             []string        `json:"tools"`
		MaxSteps          int             `json:"max_steps"`
		HandoffSchemaName string          `json:"handoff_schema_name"`
		HandoffSchema     json.RawMessage `json:"handoff_schema"`
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
		CallID:            call.CallID,
		RunID:             call.RunID,
		StepID:            call.StepID,
		Task:              a.Task,
		Provider:          a.Provider,
		Model:             a.Model,
		Tools:             a.Tools,
		MaxSteps:          a.MaxSteps,
		HandoffSchemaName: a.HandoffSchemaName,
		HandoffSchema:     a.HandoffSchema,
		WorkspaceDir:      call.WorkspaceDir,
		StateDir:          call.StateDir,
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
		Structured: agentToolPayload("", res, output),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func agentToolPayload(agent string, res AgentRunResult, output string) json.RawMessage {
	payload := agentToolPayloadMap(agent, res, output)
	raw, _ := json.Marshal(payload)
	return json.RawMessage(raw)
}

func agentToolPayloadMap(agent string, res AgentRunResult, output string) map[string]any {
	payload := map[string]any{
		"ok":           res.OK,
		"agent":        strings.TrimSpace(agent),
		"agent_run_id": res.AgentRunID,
		"used_steps":   res.UsedSteps,
		"used_tokens":  res.UsedTokens,
		"cost_usd":     res.CostUSD,
		"result":       output,
	}
	if payload["agent"] == "" {
		delete(payload, "agent")
	}
	if len(res.Structured) > 0 && json.Valid(res.Structured) {
		var handoff any
		if json.Unmarshal(res.Structured, &handoff) == nil {
			payload["handoff"] = handoff
		}
	}
	if len(res.Diagnostics) > 0 {
		payload["diagnostics"] = append([]string(nil), res.Diagnostics...)
	}
	return payload
}

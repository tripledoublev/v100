package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

type reflectTool struct{}

// NewReflect creates a new 'reflect' tool.
func NewReflect() Tool {
	return &reflectTool{}
}

func (t *reflectTool) Name() string             { return "reflect" }
func (t *reflectTool) Description() string      { return "Perform meta-cognitive self-critique. Pause to evaluate progress, plan correctness, and goal alignment." }
func (t *reflectTool) DangerLevel() DangerLevel { return Safe }
func (t *reflectTool) Effects() ToolEffects {
	return ToolEffects{
		NeedsNetwork: true,
	}
}

func (t *reflectTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["focus"],
		"properties": {
			"focus":       {"type": "string", "description": "What specific aspect to reflect on (e.g. 'recent errors', 'plan feasibility', 'goal alignment')."},
			"constraints": {"type": "string", "description": "Optional list of constraints or requirements to check against."}
		}
	}`)
}

func (t *reflectTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"verdict":   {"type": "string", "description": "PASS, FAIL, or PARTIAL"},
			"reasoning": {"type": "string", "description": "Detailed self-critique findings."},
			"pivot":     {"type": "string", "description": "Suggested course correction, if any."}
		}
	}`)
}

const reflectSystemPrompt = `You are an agent's Internal Reflection Module. 
Your task is to perform a rigorous self-critique of the agent's current trajectory.

### Reflection Focus:
{{focus}}

### Constraints:
{{constraints}}

### Instructions:
1. Analyze the recent steps and the current state of the workspace.
2. Be honest and critical. Identify any logical loops, missing information, or inefficient tool use.
3. Determine if the current plan is still valid or needs a major pivot.

Reply in the following format:
VERDICT: <PASS/FAIL/PARTIAL>
REASONING: <concise findings>
PIVOT: <suggested next step or plan adjustment>`

func (t *reflectTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		Focus       string `json:"focus"`
		Constraints string `json:"constraints"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}

	if call.Provider == nil {
		return failResult(start, "no provider available for reflection"), nil
	}

	// For reflection, we don't send the full history yet (it's already in the parent context)
	// but we ask the model to look inward.
	prompt := strings.ReplaceAll(reflectSystemPrompt, "{{focus}}", a.Focus)
	constraints := a.Constraints
	if constraints == "" {
		constraints = "None specified."
	}
	prompt = strings.ReplaceAll(prompt, "{{constraints}}", constraints)

	resp, err := call.Provider.Complete(ctx, providers.CompleteRequest{
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.0),
		},
	})
	if err != nil {
		return failResult(start, "reflection failed: "+err.Error()), nil
	}

	return ToolResult{
		OK:         true,
		Output:     resp.AssistantText,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func ptrFloat64(v float64) *float64 { return &v }

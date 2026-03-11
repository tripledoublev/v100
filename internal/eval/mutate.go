package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

// MutationResult holds the suggested prompt mutation.
type MutationResult struct {
	OriginalPrompt string `json:"original_prompt"`
	MutatedPrompt  string `json:"mutated_prompt"`
	Rationale      string `json:"rationale"`
}

const mutationSystemPrompt = `You are the v100 Prompt Optimizer. 
Your goal is to mutate an agent's initial prompt to prevent behavioral failures detected in a previous run.

### Original Prompt:
{{original}}

### Behavioral Analysis Findings:
{{analysis}}

### Instructions:
1. Analyze the findings (e.g. thrashing, context pressure, tool errors).
2. Rewrite the prompt to be more specific, add constraints, or provide better guidance to avoid these issues.
3. Keep the original intent of the task intact.

Reply in the following format:
MUTATED PROMPT: <new prompt text>
RATIONALE: <why this change prevents the detected failure>`

// MutatePrompt analyzes a run and suggests a better prompt.
func MutatePrompt(ctx context.Context, prov providers.Provider, model string, events []core.Event) (MutationResult, error) {
	report := AnalyzeTrajectory(events)
	
	originalPrompt := ""
	for _, ev := range events {
		if ev.Type == core.EventUserMsg {
			var p core.UserMsgPayload
			_ = json.Unmarshal(ev.Payload, &p)
			originalPrompt = p.Content
			break
		}
	}

	if originalPrompt == "" {
		return MutationResult{}, fmt.Errorf("could not find original prompt in trace")
	}

	if len(report.Labels) == 0 && report.ToolErrors == 0 {
		return MutationResult{
			OriginalPrompt: originalPrompt,
			MutatedPrompt:  originalPrompt,
			Rationale:      "No behavioral issues detected; original prompt is likely fine.",
		}, nil
	}

	var analysisSb strings.Builder
	for _, l := range report.Labels {
		fmt.Fprintf(&analysisSb, "- [%s] %s\n", l.Name, l.Evidence)
	}
	if report.ToolErrors > 0 {
		fmt.Fprintf(&analysisSb, "- %d tool execution errors occurred.\n", report.ToolErrors)
	}

	prompt := strings.ReplaceAll(mutationSystemPrompt, "{{original}}", originalPrompt)
	prompt = strings.ReplaceAll(prompt, "{{analysis}}", analysisSb.String())

	resp, err := prov.Complete(ctx, providers.CompleteRequest{
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		Model: model,
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.7), // allow some creative mutation
		},
	})
	if err != nil {
		return MutationResult{}, fmt.Errorf("optimizer call failed: %w", err)
	}

	text := resp.AssistantText
	mutated := originalPrompt
	rationale := "LLM did not provide structured rationale."

	if idx := strings.Index(strings.ToUpper(text), "MUTATED PROMPT:"); idx != -1 {
		rest := text[idx+len("MUTATED PROMPT:"):]
		if rIdx := strings.Index(strings.ToUpper(rest), "RATIONALE:"); rIdx != -1 {
			mutated = strings.TrimSpace(rest[:rIdx])
			rationale = strings.TrimSpace(rest[rIdx+len("RATIONALE:"):])
		} else {
			mutated = strings.TrimSpace(rest)
		}
	}

	return MutationResult{
		OriginalPrompt: originalPrompt,
		MutatedPrompt:  mutated,
		Rationale:      rationale,
	}, nil
}

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

// PolicyMutationResult holds the suggested policy mutation.
type PolicyMutationResult struct {
	OriginalPolicy string `json:"original_policy"`
	MutatedPolicy  string `json:"mutated_policy"`
	Rationale      string `json:"rationale"`
}

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

### Quantitative Failure Signature:
- Total steps: {{steps}}
- Tool failures: {{errors}}
- Total tokens consumed: {{tokens}}
- Context saturation: {{saturation}}% (at end of run)

### Instructions:
1. Analyze both the qualitative findings and the quantitative signatures.
2. Rewrite the prompt to be more specific, add constraints, or provide better guidance to avoid these issues.
3. If context saturation is high (>80%), suggest instructions for more concise tool usage or periodic summarization.
4. Keep the original intent of the task intact.

Reply in the following format:
MUTATED PROMPT: <new prompt text>
RATIONALE: <why this change prevents the detected failure>`

// MutatePrompt analyzes a run and suggests a better prompt.
func MutatePrompt(ctx context.Context, prov providers.Provider, model string, events []core.Event) (MutationResult, error) {
	report := AnalyzeTrajectory(events)
	stats := core.ComputeStats(events)
	
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

	if len(report.Labels) == 0 && report.ToolErrors == 0 && stats.ToolFailures == 0 {
		return MutationResult{
			OriginalPrompt: originalPrompt,
			MutatedPrompt:  originalPrompt,
			Rationale:      "No behavioral issues or tool failures detected; original prompt is likely fine.",
		}, nil
	}

	var analysisSb strings.Builder
	for _, l := range report.Labels {
		fmt.Fprintf(&analysisSb, "- [%s] %s\n", l.Name, l.Evidence)
	}
	if report.ToolErrors > 0 {
		fmt.Fprintf(&analysisSb, "- %d tool execution errors (e.g. non-existent tools) occurred.\n", report.ToolErrors)
	}

	prompt := strings.ReplaceAll(mutationSystemPrompt, "{{original}}", originalPrompt)
	prompt = strings.ReplaceAll(prompt, "{{analysis}}", analysisSb.String())
	
	// Inject quantitative metrics
	prompt = strings.ReplaceAll(prompt, "{{steps}}", fmt.Sprintf("%d", stats.TotalSteps))
	prompt = strings.ReplaceAll(prompt, "{{errors}}", fmt.Sprintf("%d", stats.ToolFailures))
	prompt = strings.ReplaceAll(prompt, "{{tokens}}", fmt.Sprintf("%d", stats.TokensIn+stats.TokensOut))
	
	saturation := 0.0
	if stats.ModelMetadata.ContextSize > 0 {
		saturation = float64(stats.TokensIn) / float64(stats.ModelMetadata.ContextSize) * 100
	}
	prompt = strings.ReplaceAll(prompt, "{{saturation}}", fmt.Sprintf("%.1f", saturation))

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

const policyMutationSystemPrompt = `You are the v100 Policy Optimizer.
Your goal is to rewrite an agent's system policy (identity, constraints, workflow rules) to prevent behavioral failures detected in a previous run.

### Current System Policy:
{{policy}}

### Behavioral Analysis Findings:
{{analysis}}

### Quantitative Failure Signature:
- Total steps: {{steps}}
- Tool failures: {{errors}}
- Total tokens consumed: {{tokens}}
- Context saturation: {{saturation}}% (at end of run)

### Instructions:
1. Analyze both the qualitative findings and the quantitative signatures.
2. Rewrite the system policy to add constraints, guardrails, or workflow rules that prevent these issues.
3. If tool hallucination is detected, add explicit tool usage guidelines.
4. If thrashing is detected, add loop-breaking or deduplication rules.
5. If context pressure is high (>80%), add rules for concise tool usage or periodic summarization.
6. Preserve the agent's core identity and capabilities. Only add or modify rules that address detected issues.

Reply in the following format:
MUTATED POLICY: <new system policy text>
RATIONALE: <why each change prevents the detected failure>`

// MutatePolicy analyzes a run and suggests an improved system policy.
func MutatePolicy(ctx context.Context, prov providers.Provider, model string, currentPolicy string, events []core.Event) (PolicyMutationResult, error) {
	report := AnalyzeTrajectory(events)
	stats := core.ComputeStats(events)

	if len(report.Labels) == 0 && report.ToolErrors == 0 && stats.ToolFailures == 0 {
		return PolicyMutationResult{
			OriginalPolicy: currentPolicy,
			MutatedPolicy:  currentPolicy,
			Rationale:      "No behavioral issues or tool failures detected; current policy is likely fine.",
		}, nil
	}

	var analysisSb strings.Builder
	for _, l := range report.Labels {
		fmt.Fprintf(&analysisSb, "- [%s] %s\n", l.Name, l.Evidence)
	}
	if report.ToolErrors > 0 {
		fmt.Fprintf(&analysisSb, "- %d tool execution errors occurred.\n", report.ToolErrors)
	}

	prompt := strings.ReplaceAll(policyMutationSystemPrompt, "{{policy}}", currentPolicy)
	prompt = strings.ReplaceAll(prompt, "{{analysis}}", analysisSb.String())
	prompt = strings.ReplaceAll(prompt, "{{steps}}", fmt.Sprintf("%d", stats.TotalSteps))
	prompt = strings.ReplaceAll(prompt, "{{errors}}", fmt.Sprintf("%d", stats.ToolFailures))
	prompt = strings.ReplaceAll(prompt, "{{tokens}}", fmt.Sprintf("%d", stats.TokensIn+stats.TokensOut))

	saturation := 0.0
	if stats.ModelMetadata.ContextSize > 0 {
		saturation = float64(stats.TokensIn) / float64(stats.ModelMetadata.ContextSize) * 100
	}
	prompt = strings.ReplaceAll(prompt, "{{saturation}}", fmt.Sprintf("%.1f", saturation))

	resp, err := prov.Complete(ctx, providers.CompleteRequest{
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		Model: model,
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.7),
		},
	})
	if err != nil {
		return PolicyMutationResult{}, fmt.Errorf("policy optimizer call failed: %w", err)
	}

	text := resp.AssistantText
	mutated := currentPolicy
	rationale := "LLM did not provide structured rationale."

	if idx := strings.Index(strings.ToUpper(text), "MUTATED POLICY:"); idx != -1 {
		rest := text[idx+len("MUTATED POLICY:"):]
		if rIdx := strings.Index(strings.ToUpper(rest), "RATIONALE:"); rIdx != -1 {
			mutated = strings.TrimSpace(rest[:rIdx])
			rationale = strings.TrimSpace(rest[rIdx+len("RATIONALE:"):])
		} else {
			mutated = strings.TrimSpace(rest)
		}
	}

	return PolicyMutationResult{
		OriginalPolicy: currentPolicy,
		MutatedPolicy:  mutated,
		Rationale:      rationale,
	}, nil
}

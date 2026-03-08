package core

import (
	"context"
	"fmt"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// ReactSolver implements the classic ReAct loop.
type ReactSolver struct{}

func (s *ReactSolver) Name() string { return "react" }

func (s *ReactSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
	stepID := newID()
	stepStart := time.Now()
	budgetBefore := l.Budget.Budget()
	var modelCalls int

	// 1. Append user message
	_, err := l.emit(EventUserMsg, stepID, UserMsgPayload{Content: userInput})
	if err != nil {
		return SolveResult{}, err
	}
	l.Messages = append(l.Messages, providers.Message{Role: "user", Content: userInput})

	// 2. Maybe compress history before calling the provider.
	if l.Policy != nil && l.Policy.ContextLimit > 0 {
		_ = l.maybeCompress(ctx, stepID) // best-effort; log but don't fail
	}

	// 3. Continue model/tool turns until the model produces a final (no-tool) response.
	maxToolCalls := 20
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string

	for {
		msgs := l.buildMessages()
		toolSpecs := l.Tools.Specs()
		toolNames := make([]string, 0, len(toolSpecs))
		for _, ts := range toolSpecs {
			toolNames = append(toolNames, ts.Name)
		}
		_, _ = l.emit(EventModelCall, stepID, ModelCallPayload{
			Model:        "",
			Messages:     msgs,
			ToolNames:    toolNames,
			MaxToolCalls: maxToolCalls - toolCallsUsed,
		})
		t0 := time.Now()
		resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
			RunID:     l.Run.ID,
			StepID:    stepID,
			Messages:  msgs,
			Tools:     toolSpecs,
			Model:     "",
			GenParams: l.GenParams,
			Hints: providers.Hints{
				MaxToolCalls: maxToolCalls - toolCallsUsed,
			},
		})
		durMS := time.Since(t0).Milliseconds()
		modelCalls++
		if err != nil {
			_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
			l.emitErrorAssistance(ctx, stepID, err)
			return SolveResult{}, fmt.Errorf("provider: %w", err)
		}

		if err := l.Budget.AddTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens); err != nil {
			return SolveResult{}, err
		}
		if err := l.Budget.AddCost(resp.Usage.CostUSD); err != nil {
			return SolveResult{}, err
		}

		tcPayload := make([]ToolCall, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			tcPayload[i] = ToolCall{ID: tc.ID, Name: tc.Name, ArgsJSON: string(tc.Args)}
		}
		_, err = l.emit(EventModelResp, stepID, ModelRespPayload{
			Text:      resp.AssistantText,
			ToolCalls: tcPayload,
			Usage: Usage{
				InputTokens:  resp.Usage.InputTokens,
				OutputTokens: resp.Usage.OutputTokens,
				CostUSD:      resp.Usage.CostUSD,
			},
			DurationMS: durMS,
		})
		if err != nil {
			return SolveResult{}, err
		}

		l.Messages = append(l.Messages, providers.Message{
			Role:      "assistant",
			Content:   resp.AssistantText,
			ToolCalls: resp.ToolCalls,
		})

		finalText = resp.AssistantText

		if len(resp.ToolCalls) == 0 {
			break
		}

		for _, tc := range resp.ToolCalls {
			if toolCallsUsed >= maxToolCalls {
				_, _ = l.emit(EventRunError, stepID, RunErrorPayload{
					Error: fmt.Sprintf("max tool calls per step reached (%d)", maxToolCalls),
				})
				break
			}
			if err := l.execToolCall(ctx, stepID, tc); err != nil {
				return SolveResult{}, err
			}
			toolCallsUsed++
		}
		if toolCallsUsed >= maxToolCalls {
			break
		}
	}

	// Emit step summary before incrementing step budget
	budgetAfter := l.Budget.Budget()
	l.stepCount++
	_, _ = l.emit(EventStepSummary, stepID, StepSummaryPayload{
		StepNumber:   l.stepCount,
		InputTokens:  budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		OutputTokens: 0, // tracked in aggregate via UsedTokens
		CostUSD:      budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
		ToolCalls:    toolCallsUsed,
		ModelCalls:   modelCalls,
		DurationMS:   time.Since(stepStart).Milliseconds(),
	})

	// Increment step budget
	if err := l.Budget.AddStep(); err != nil {
		return SolveResult{}, err
	}

	return SolveResult{
		FinalText: finalText,
		Steps:     1,
		Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
	}, nil
}

package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// RouterSolver dynamically switches between a cheap and a smart provider.
type RouterSolver struct {
	Cheap providers.Provider
	Smart providers.Provider
}

func (s *RouterSolver) Name() string { return "router" }

func (s *RouterSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
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
		_ = l.maybeCompress(ctx, stepID)
	}

	maxToolCalls := 50
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string

	// Current provider for this turn
	currentProv := s.Cheap
	if currentProv == nil {
		currentProv = l.Provider
	}
	smartMode := false

	for {
		msgs := l.buildMessages()
		toolSpecs := l.Tools.Specs()
		toolNames := make([]string, 0, len(toolSpecs))
		for _, ts := range toolSpecs {
			toolNames = append(toolNames, ts.Name)
		}

		// Turn start
		_, _ = l.emit(EventModelCall, stepID, ModelCallPayload{
			Model:        "",
			Messages:     msgs,
			ToolNames:    toolNames,
			MaxToolCalls: maxToolCalls - toolCallsUsed,
		})

		var (
			assistantText strings.Builder
			toolCalls     []providers.ToolCall
			usage         providers.Usage
			durMS         int64
			t0            = time.Now()
		)

		// Router Logic: Always try Cheap first unless already in SmartMode
		resp, err := currentProv.Complete(ctx, providers.CompleteRequest{
			RunID:     l.Run.ID,
			StepID:    stepID,
			Messages:  msgs,
			Tools:     toolSpecs,
			GenParams: l.GenParams,
			Hints: providers.Hints{
				MaxToolCalls: maxToolCalls - toolCallsUsed,
			},
		})
		durMS = time.Since(t0).Milliseconds()

		if err != nil {
			_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
			l.emitErrorAssistance(ctx, stepID, err)
			return SolveResult{}, fmt.Errorf("router solver: %w", err)
		}

		// Check if we need to escalate
		needsEscalation := false
		if !smartMode && s.Smart != nil {
			for _, tc := range resp.ToolCalls {
				effects := l.Tools.Effects(tc.Name)
				if effects.MutatesWorkspace || effects.ExternalSideEffect || effects.MutatesRunState {
					needsEscalation = true
					break
				}
			}
		}

		if needsEscalation {
			// Escalate to Smart Model
			_, _ = l.emit(EventModelToken, stepID, map[string]string{"text": "[escalating to smart model...]\n"})
			smartMode = true
			currentProv = s.Smart
			t0 = time.Now()
			resp, err = currentProv.Complete(ctx, providers.CompleteRequest{
				RunID:     l.Run.ID,
				StepID:    stepID,
				Messages:  msgs,
				Tools:     toolSpecs,
				GenParams: l.GenParams,
				Hints: providers.Hints{
					MaxToolCalls: maxToolCalls - toolCallsUsed,
				},
			})
			durMS = time.Since(t0).Milliseconds()
			if err != nil {
				return SolveResult{}, fmt.Errorf("smart provider: %w", err)
			}
		}

		assistantText.WriteString(resp.AssistantText)
		toolCalls = resp.ToolCalls
		usage = resp.Usage

		if err := l.Budget.AddTokens(usage.InputTokens, usage.OutputTokens); err != nil {
			return SolveResult{}, err
		}
		if err := l.Budget.AddCost(usage.CostUSD); err != nil {
			return SolveResult{}, err
		}

		tcPayload := make([]ToolCall, len(toolCalls))
		for i, tc := range toolCalls {
			tcPayload[i] = ToolCall{ID: tc.ID, Name: tc.Name, ArgsJSON: string(tc.Args)}
		}
		_, err = l.emit(EventModelResp, stepID, ModelRespPayload{
			Text:      assistantText.String(),
			ToolCalls: tcPayload,
			Usage: Usage{
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				CostUSD:      usage.CostUSD,
			},
			DurationMS: durMS,
		})
		if err != nil {
			return SolveResult{}, err
		}

		l.Messages = append(l.Messages, providers.Message{
			Role:      "assistant",
			Content:   assistantText.String(),
			ToolCalls: toolCalls,
		})

		finalText = assistantText.String()
		modelCalls++

		if len(toolCalls) == 0 {
			break
		}

		for _, tc := range toolCalls {
			if toolCallsUsed >= maxToolCalls {
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

	budgetAfter := l.Budget.Budget()
	l.stepCount++
	_, _ = l.emit(EventStepSummary, stepID, StepSummaryPayload{
		StepNumber:   l.stepCount,
		InputTokens:  budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:      budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
		ToolCalls:    toolCallsUsed,
		ModelCalls:   modelCalls,
		DurationMS:   time.Since(stepStart).Milliseconds(),
	})

	_ = l.Budget.AddStep()

	return SolveResult{
		FinalText: finalText,
		Steps:     1,
		Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
	}, nil
}

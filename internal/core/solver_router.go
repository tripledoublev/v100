package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// trivialMutations lists tools that mutate the workspace but are de facto
// safe and should NOT trigger escalation to the frontier model. Any tool
// not in this map is subject to escalation if it carries MutatesWorkspace,
// ExternalSideEffect, or MutatesRunState.
var trivialMutations = map[string]bool{
	"fs_mkdir": true, // creating directories is idempotent and reversible
}

func routerToolNeedsEscalation(l *Loop, toolName string) bool {
	if l == nil || l.Tools == nil {
		return true
	}
	tool, ok := l.Tools.Get(toolName)
	if !ok {
		// Unknown or disabled tools from the cheap tier should be retried on
		// the smart tier instead of being allowed to hallucinate into failure.
		return true
	}
	effects := tool.Effects()
	if effects.MutatesWorkspace || effects.ExternalSideEffect || effects.MutatesRunState {
		return !trivialMutations[toolName]
	}
	return false
}

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
	if err := l.appendUserMessage(stepID, userInput); err != nil {
		return SolveResult{}, err
	}

	// 1b. Sanitize unresolved tool calls from live history before next provider request.
	_ = l.SanitizeLiveMessages() // idempotent; no error handling needed

	// 2. Maybe compress history before calling the provider.
	_ = l.maybeCompress(ctx, stepID)

	maxToolCalls := 50
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string
	toolsStopped := false

	// Current provider for this turn
	currentProv := s.Cheap
	if currentProv == nil {
		currentProv = l.Provider
	}
	smartMode := false

	for {
		select {
		case <-ctx.Done():
			return SolveResult{}, ctx.Err()
		default:
		}

		msgs := l.buildMessagesForStep(stepID)
		var toolSpecs []providers.ToolSpec
		if !toolsStopped {
			toolSpecs = l.Tools.Specs()
		}
		toolNames := make([]string, 0, len(toolSpecs))
		for _, ts := range toolSpecs {
			toolNames = append(toolNames, ts.Name)
		}

		// Turn start
		_, _ = l.emit(EventModelCall, stepID, newModelCallPayload("", msgs, toolNames, maxToolCalls-toolCallsUsed, currentProv))

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
				if routerToolNeedsEscalation(l, tc.Name) {
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

		// ── Intervention Hooks ──────────────────────────────────────────
		if hres := l.runHooks(stepID, HookStageModelResponse); hres.Action != HookContinue {
			restartLoop, err := l.applyHookResult(hres, "user", &toolsStopped)
			if err != nil {
				return SolveResult{FinalText: finalText, Steps: 1}, err
			}
			if restartLoop {
				continue
			}
		}

		if toolsStopped {
			toolCalls = nil
		}
		if len(toolCalls) == 0 {
			break
		}

		postToolRestart := false
		for _, tc := range toolCalls {
			if toolCallsUsed >= maxToolCalls {
				break
			}
			if _, err := l.execToolCall(ctx, stepID, tc); err != nil {
				return SolveResult{}, err
			}
			toolCallsUsed++
			if hres := l.runHooks(stepID, HookStageToolResult); hres.Action != HookContinue {
				restartLoop, err := l.applyHookResult(hres, "system", &toolsStopped)
				if err != nil {
					return SolveResult{FinalText: finalText, Steps: 1}, err
				}
				if restartLoop {
					postToolRestart = true
					break
				}
			}
		}
		if postToolRestart {
			continue
		}
		if toolCallsUsed >= maxToolCalls {
			break
		}
	}

	budgetAfter := l.Budget.Budget()
	l.stepCount++
	_, _ = l.emit(EventStepSummary, stepID, StepSummaryPayload{
		StepNumber:  l.stepCount,
		InputTokens: budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:     budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
		ToolCalls:   toolCallsUsed,
		ModelCalls:  modelCalls,
		DurationMS:  time.Since(stepStart).Milliseconds(),
	})

	_ = l.Budget.AddStep()

	return SolveResult{
		FinalText: finalText,
		Steps:     1,
		Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
	}, nil
}

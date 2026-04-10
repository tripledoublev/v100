package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// MiniGLMSolver intelligently switches between MiniMax (tool-focused) and GLM (reasoning-focused).
// Logic: If current response has tool calls, use MiniMax next (to execute well).
// If current response is pure reasoning, use GLM next (better planning).
type MiniGLMSolver struct {
	MiniMax providers.Provider
	GLM     providers.Provider
}

func (s *MiniGLMSolver) Name() string { return "miniglm" }

func (s *MiniGLMSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
	stepID := newID()
	stepStart := time.Now()
	budgetBefore := l.Budget.Budget()
	var modelCalls int

	// 1. Append user message
	if err := l.appendUserMessage(stepID, userInput); err != nil {
		return SolveResult{}, err
	}

	// 1b. Sanitize unresolved tool calls from live history before next provider request.
	_ = l.SanitizeLiveMessages()

	// 2. Maybe compress history before calling the provider.
	_ = l.maybeCompress(ctx, stepID)

	maxToolCalls := 50
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string
	toolsStopped := false

	// Start with MiniMax (good for action/tool generation)
	currentProv := s.MiniMax
	if currentProv == nil {
		currentProv = l.Provider
	}

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
			if !errors.Is(err, context.Canceled) {
				_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
				l.emitErrorAssistance(ctx, stepID, err)
			}
			return SolveResult{}, fmt.Errorf("miniglm solver: %w", err)
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

		// Decide next provider based on tool calls in response
		// If response has tool calls: stay with MiniMax (good at action)
		// If response is pure reasoning: switch to GLM next iteration (better reasoning)
		if len(toolCalls) == 0 && currentProv == s.MiniMax && s.GLM != nil {
			// Pure reasoning turn: switch to GLM for better planning on next iteration
			currentProv = s.GLM
		} else if len(toolCalls) > 0 && currentProv == s.GLM && s.MiniMax != nil {
			// Tool call turn: switch back to MiniMax for execution on next iteration
			currentProv = s.MiniMax
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

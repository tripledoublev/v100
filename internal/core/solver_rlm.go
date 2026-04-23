package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// RLMSolver implements the Recursive Language Model pattern where the agent
// can call sub-LMs with typed signatures via a `predict` tool.
type RLMSolver struct {
	SubProvider            providers.Provider
	MaxConcurrentPredicts  int
	EnableVision           bool
}

func (s *RLMSolver) Name() string { return "rlm" }

func (s *RLMSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
	stepID := newID()
	stepStart := time.Now()
	budgetBefore := l.Budget.Budget()
	var modelCalls int

	if err := l.appendUserMessage(stepID, userInput); err != nil {
		return SolveResult{}, err
	}
	_ = l.SanitizeLiveMessages()
	_ = l.maybeCompress(ctx, stepID)

	maxToolCalls := 50
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}

	var stepOutputTokens int
	toolCallsUsed := 0
	var finalText string
	toolsStopped := false
	predictToolName := "predict"

	predictToolSchema := `{"type":"object","properties":{"signature":{"type":"string","description":"DSPy signature (e.g., 'img: dspy.Image, question: str -> answer: str')"}},"required":["signature"]}`
	predictTool := providers.ToolSpec{
		Name:        predictToolName,
		Description: `Call a sub-model with a DSPy-style signature. Format: predict("input: Type, ... -> output: Type", input=value, ...).`,
		InputSchema:  json.RawMessage(predictToolSchema),
	}

	for {
		select {
		case <-ctx.Done():
			return SolveResult{}, ctx.Err()
		default:
		}

		msgs := l.buildMessagesForStep(stepID)
		toolSpecs := l.Tools.Specs()
		allToolSpecs := append(toolSpecs, predictTool)

		toolNames := make([]string, 0, len(allToolSpecs))
		for _, ts := range allToolSpecs {
			toolNames = append(toolNames, ts.Name)
		}

		_, _ = l.emit(EventModelCall, stepID, newModelCallPayload("", msgs, toolNames, maxToolCalls-toolCallsUsed, l.Provider))

		var (
			assistantText strings.Builder
			toolCalls     []providers.ToolCall
			usage         providers.Usage
			durMS         int64
			t0            = time.Now()
		)

		streamer, isStreamer := l.Provider.(providers.Streamer)
		if isStreamer && l.Policy != nil && l.Policy.Streaming {
			ch, err := streamer.StreamComplete(ctx, providers.CompleteRequest{
				RunID:     l.Run.ID,
				StepID:    stepID,
				Messages:  msgs,
				Tools:     allToolSpecs,
				GenParams: l.GenParams,
				Hints:     providers.Hints{MaxToolCalls: maxToolCalls - toolCallsUsed},
			})
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
					l.emitErrorAssistance(ctx, stepID, err)
				}
				return SolveResult{}, fmt.Errorf("provider stream: %w", err)
			}

			for ev := range ch {
				switch ev.Type {
				case providers.StreamToken:
					assistantText.WriteString(ev.Text)
					_, _ = l.emit(EventModelToken, stepID, map[string]string{"text": ev.Text})
				case providers.StreamToolCallStart:
					toolCalls = append(toolCalls, providers.ToolCall{ID: ev.ToolCallID, Name: ev.ToolCallName})
				case providers.StreamToolCallDelta:
					if len(toolCalls) > 0 {
						last := &toolCalls[len(toolCalls)-1]
						last.Args = append(last.Args, ev.ToolCallArgs...)
					}
				case providers.StreamDone:
					usage = ev.Usage
				case providers.StreamError:
					return SolveResult{}, ev.Err
				}
			}
			durMS = time.Since(t0).Milliseconds()
		} else {
			resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
				RunID:     l.Run.ID,
				StepID:    stepID,
				Messages:  msgs,
				Tools:     allToolSpecs,
				GenParams: l.GenParams,
				Hints:     providers.Hints{MaxToolCalls: maxToolCalls - toolCallsUsed},
			})
			durMS = time.Since(t0).Milliseconds()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
					l.emitErrorAssistance(ctx, stepID, err)
				}
				return SolveResult{}, fmt.Errorf("provider: %w", err)
			}
			assistantText.WriteString(resp.AssistantText)
			toolCalls = resp.ToolCalls
			usage = resp.Usage
		}

		if text := assistantText.String(); strings.Contains(text, "<invoke") {
			cleaned, extracted := providers.ExtractTextualToolCalls(text)
			if len(extracted) > 0 || cleaned != text {
				assistantText.Reset()
				assistantText.WriteString(cleaned)
				toolCalls = append(toolCalls, extracted...)
			}
		}

		_ = l.Budget.AddTokens(usage.InputTokens, usage.OutputTokens)
		_ = l.Budget.AddCost(usage.CostUSD)
		stepOutputTokens = usage.OutputTokens

		tcPayload := make([]ToolCall, len(toolCalls))
		for i, tc := range toolCalls {
			tcPayload[i] = ToolCall{ID: tc.ID, Name: tc.Name, ArgsJSON: string(tc.Args)}
		}
		if _, err := l.emit(EventModelResp, stepID, ModelRespPayload{
			Text:      assistantText.String(),
			ToolCalls: tcPayload,
			Usage: Usage{
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				CostUSD:      usage.CostUSD,
			},
			DurationMS: durMS,
			TTFT:       0,
		}); err != nil {
			return SolveResult{}, err
		}

		l.Messages = append(l.Messages, providers.Message{
			Role:      "assistant",
			Content:   assistantText.String(),
			ToolCalls: toolCalls,
		})

		finalText = assistantText.String()
		modelCalls++

		if hres := l.runHooks(stepID, HookStageModelResponse); hres.Action != HookContinue {
			if _, err := l.applyHookResult(hres, "user", &toolsStopped); err != nil {
				return SolveResult{FinalText: finalText, Steps: 1}, err
			}
		}

		if toolsStopped {
			toolCalls = nil
		}

		var predictCalls, regularCalls []providers.ToolCall
		for _, tc := range toolCalls {
			if tc.Name == predictToolName {
				predictCalls = append(predictCalls, tc)
			} else {
				regularCalls = append(regularCalls, tc)
			}
		}

		if len(predictCalls) > 0 {
			subProv := s.SubProvider
			if subProv == nil {
				subProv = l.Provider
			}
			for _, pc := range predictCalls {
				if toolCallsUsed >= maxToolCalls {
					break
				}
				result, err := s.executePredictCall(ctx, l, stepID, pc, subProv)
				toolCallsUsed++
				toolMsg := providers.Message{Role: "tool", Name: predictToolName, ToolCallID: pc.ID}
				if err != nil {
					toolMsg.Content = fmt.Sprintf("predict error: %v", err)
				} else {
					toolMsg.Content = result
				}
				l.Messages = append(l.Messages, toolMsg)
			}
		}

		for _, tc := range regularCalls {
			if toolCallsUsed >= maxToolCalls {
				break
			}
			if _, err := l.execToolCall(ctx, stepID, tc); err != nil {
				return SolveResult{}, err
			}
			toolCallsUsed++
			if hres := l.runHooks(stepID, HookStageToolResult); hres.Action != HookContinue {
				if _, err := l.applyHookResult(hres, "system", &toolsStopped); err != nil {
					return SolveResult{FinalText: finalText, Steps: 1}, err
				}
			}
		}

		if toolCallsUsed >= maxToolCalls {
			break
		}

		if len(toolCalls) == 0 {
			break
		}
	}

	budgetAfter := l.Budget.Budget()
	l.stepCount++
	_, _ = l.emit(EventStepSummary, stepID, StepSummaryPayload{
		StepNumber:   l.stepCount,
		InputTokens:  budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		OutputTokens: stepOutputTokens,
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

func (s *RLMSolver) executePredictCall(ctx context.Context, l *Loop, stepID string, tc providers.ToolCall, subProv providers.Provider) (string, error) {
	var args struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return "", fmt.Errorf("predict: parse args: %w", err)
	}
	if args.Signature == "" {
		return "", fmt.Errorf("predict: signature is required")
	}

	temp := 0.3
	subResp, err := subProv.Complete(ctx, providers.CompleteRequest{
		RunID:  l.Run.ID,
		StepID: stepID,
		Messages: []providers.Message{
			{Role: "system", Content: fmt.Sprintf("Execute predict signature: %s", args.Signature)},
			{Role: "user", Content: fmt.Sprintf("Run prediction for: %s", args.Signature)},
		},
		GenParams: providers.GenParams{Temperature: &temp},
	})
	if err != nil {
		return "", fmt.Errorf("sub-provider: %w", err)
	}

	_ = l.Budget.AddTokens(subResp.Usage.InputTokens, subResp.Usage.OutputTokens)
	_ = l.Budget.AddCost(subResp.Usage.CostUSD)

	return subResp.AssistantText, nil
}

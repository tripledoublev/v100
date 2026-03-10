package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	maxToolCalls := 50 // sensible high default
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string
	var terminalErr error

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

		var (
			assistantText strings.Builder
			toolCalls     []providers.ToolCall
			tcMap         = make(map[string]*providers.ToolCall)
			usage         providers.Usage
			durMS         int64
			ttft          int64
			t0            = time.Now()
		)

		streamer, isStreamer := l.Provider.(providers.Streamer)
		if isStreamer && l.Policy != nil && l.Policy.Streaming {
			ch, err := streamer.StreamComplete(ctx, providers.CompleteRequest{
				RunID:     l.Run.ID,
				StepID:    stepID,
				Messages:  msgs,
				Tools:     toolSpecs,
				GenParams: l.GenParams,
				Hints: providers.Hints{
					MaxToolCalls: maxToolCalls - toolCallsUsed,
				},
			})
			if err != nil {
				_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: err.Error()})
				l.emitErrorAssistance(ctx, stepID, err)
				return SolveResult{}, fmt.Errorf("provider stream: %w", err)
			}

			for ev := range ch {
				switch ev.Type {
				case providers.StreamToken:
					if ttft == 0 {
						ttft = time.Since(t0).Milliseconds()
					}
					assistantText.WriteString(ev.Text)
					_, _ = l.emit(EventModelToken, stepID, map[string]string{"text": ev.Text})
				case providers.StreamToolCallStart:
					if ttft == 0 {
						ttft = time.Since(t0).Milliseconds()
					}
					tc := &providers.ToolCall{ID: ev.ToolCallID, Name: ev.ToolCallName}
					toolCalls = append(toolCalls, *tc)
					tcMap[ev.ToolCallID] = &toolCalls[len(toolCalls)-1]
					_, _ = l.emit(EventToolCall, stepID, ToolCallPayload{
						CallID: ev.ToolCallID,
						Name:   ev.ToolCallName,
					})
				case providers.StreamToolCallDelta:
					if tc, ok := tcMap[ev.ToolCallID]; ok {
						args := string(tc.Args) + ev.ToolCallArgs
						tc.Args = json.RawMessage(args)
						_, _ = l.emit(EventToolCallDelta, stepID, ToolCallDeltaPayload{
							CallID: ev.ToolCallID,
							Delta:  ev.ToolCallArgs,
						})
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
				return SolveResult{}, fmt.Errorf("provider: %w", err)
			}
			assistantText.WriteString(resp.AssistantText)
			toolCalls = resp.ToolCalls
			usage = resp.Usage
		}

		if err := l.Budget.AddTokens(usage.InputTokens, usage.OutputTokens); err != nil && terminalErr == nil {
			terminalErr = err
		}
		if err := l.Budget.AddCost(usage.CostUSD); err != nil && terminalErr == nil {
			terminalErr = err
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
			TTFT:       ttft,
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
		if hres := l.runHooks(stepID); hres.Action != HookContinue {
			switch hres.Action {
			case HookInjectMessage:
				l.Messages = append(l.Messages, providers.Message{
					Role:    "user",
					Content: hres.Message,
				})
				// Continue the loop to let the model respond to the injected message
				continue
			case HookTerminate:
				return SolveResult{FinalText: finalText, Steps: 1}, fmt.Errorf("hook terminated: %s", hres.Reason)
			case HookForceReplan:
				// React doesn't have a plan-specific state, but we could inject a "replan" instruction
				l.Messages = append(l.Messages, providers.Message{
					Role:    "user",
					Content: "System intervention: please discard your current plan and reassess.",
				})
				continue
			}
		}

		if terminalErr != nil {
			break
		}

		if len(toolCalls) == 0 {
			break
		}

		for _, tc := range toolCalls {
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
	if err := l.Budget.AddStep(); err != nil && terminalErr == nil {
		terminalErr = err
	}

	result := SolveResult{
		FinalText: finalText,
		Steps:     1,
		Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
		CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
	}
	if terminalErr != nil {
		return result, terminalErr
	}
	return result, nil
}

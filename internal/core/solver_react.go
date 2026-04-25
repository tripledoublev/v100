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

// ReactSolver implements the classic ReAct loop.
type ReactSolver struct{}

func (s *ReactSolver) Name() string { return "react" }

const (
	inspectionWatchdogToolThreshold  = 8
	inspectionWatchdogModelThreshold = 3
	readHeavyWatchdogToolThreshold   = 6
	readHeavyWatchdogModelThreshold  = 2
	readHeavyWatchdogTokenThreshold  = 40000
)

var (
	glmStreamSilenceTimeout = 15 * time.Second
	glmStreamMaxRetries     = 1
)

type glmStreamStallError struct {
	Silence time.Duration
}

func (e *glmStreamStallError) Error() string {
	return fmt.Sprintf("glm stream stalled after partial output: no tokens for %s", e.Silence.Round(time.Second))
}

func (s *ReactSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
	stepID := newID()
	stepStart := time.Now()
	budgetBefore := l.Budget.Budget()
	var modelCalls int

	// 0. Pre-step budget check — warn if budget is nearly exhausted
	if b := l.Budget.Budget(); b.MaxTokens > 0 && b.UsedTokens > 0 {
		remaining := b.MaxTokens - b.UsedTokens
		// Only pre-reject if remaining is both <2000 and <5% of total budget
		threshold := b.MaxTokens / 20
		if threshold > 2000 {
			threshold = 2000
		}
		if remaining > 0 && remaining < threshold {
			_, _ = l.emit(EventRunError, stepID, RunErrorPayload{
				Error: fmt.Sprintf("token budget nearly exhausted (%d/%d used) — not enough for another step", b.UsedTokens, b.MaxTokens),
			})
			return SolveResult{}, &ErrBudgetExceeded{Reason: fmt.Sprintf("tokens nearly exhausted: %d/%d", b.UsedTokens, b.MaxTokens)}
		}
	}
	if b := l.Budget.Budget(); b.MaxSteps > 0 && b.UsedSteps >= b.MaxSteps {
		return SolveResult{}, &ErrBudgetExceeded{Reason: fmt.Sprintf("steps exhausted: %d/%d", b.UsedSteps, b.MaxSteps)}
	}

	// 1. Append user message
	if err := l.appendUserMessage(stepID, userInput); err != nil {
		return SolveResult{}, err
	}

	// 1b. Sanitize unresolved tool calls from live history before next provider request.
	// This prevents MiniMax error 2013 when long-running tool calls haven't completed.
	_ = l.SanitizeLiveMessages() // idempotent; no error handling needed

	// 2. Maybe compress history before calling the provider.
	_ = l.maybeCompress(ctx, stepID) // best-effort; log but don't fail

	// 3. Continue model/tool turns until the model produces a final (no-tool) response.
	maxToolCalls := 50 // sensible high default
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0
	var finalText string
	var terminalErr error
	inspectionOnly := true
	inspectionToolCalls := 0
	stepTokensUsed := 0
	stepOutputTokens := 0
	watchdogInjected := false
	toolsStopped := false
	denialCounts := map[string]int{} // key: "toolName:args" → denial count

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
		_, _ = l.emit(EventModelCall, stepID, newModelCallPayload("", msgs, toolNames, maxToolCalls-toolCallsUsed, l.Provider))

		var (
			assistantText strings.Builder
			toolCalls     []providers.ToolCall
			tcMap         map[string]*providers.ToolCall
			usage         providers.Usage
			durMS         int64
			ttft          int64
			t0            = time.Now()
		)

		streamer, isStreamer := l.Provider.(providers.Streamer)
		if isStreamer && l.Policy != nil && l.Policy.Streaming {
			var streamErr error
			for attempt := 0; ; attempt++ {
				assistantText.Reset()
				toolCalls = nil
				tcMap = make(map[string]*providers.ToolCall)
				usage = providers.Usage{}
				ttft = 0
				t0 = time.Now()

				streamCtx := ctx
				cancelStream := func() {}
				if shouldWatchGLMStream(l.Provider) {
					var cancel context.CancelFunc
					streamCtx, cancel = context.WithCancel(ctx)
					cancelStream = cancel
				}

				ch, err := streamer.StreamComplete(streamCtx, providers.CompleteRequest{
					RunID:     l.Run.ID,
					StepID:    stepID,
					Messages:  msgs,
					Tools:     toolSpecs,
					Model:     l.Model,
					GenParams: l.GenParams,
					Hints: providers.Hints{
						MaxToolCalls: maxToolCalls - toolCallsUsed,
					},
				})
				if err != nil {
					cancelStream()
					streamErr = err
					break
				}

				sawToken := false
				streamErr = nil
				for {
					var watchdog <-chan time.Time
					if shouldWatchGLMStream(l.Provider) && sawToken {
						watchdog = time.After(glmStreamSilenceTimeout)
					}

					select {
					case <-ctx.Done():
						cancelStream()
						return SolveResult{}, ctx.Err()
					case <-watchdog:
						cancelStream()
						streamErr = &glmStreamStallError{Silence: glmStreamSilenceTimeout}
					case ev, ok := <-ch:
						if !ok {
							cancelStream()
							durMS = time.Since(t0).Milliseconds()
							if streamErr != nil {
								break
							}
							goto streamFinished
						}
						switch ev.Type {
						case providers.StreamToken:
							sawToken = true
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
							cancelStream()
							streamErr = ev.Err
						}
					}

					if streamErr != nil {
						break
					}
				}

				if _, ok := streamErr.(*glmStreamStallError); ok && attempt < glmStreamMaxRetries {
					_, _ = l.emit(EventHookIntervention, stepID, HookInterventionPayload{
						Action:  "stream_retry",
						Message: fmt.Sprintf("%s; retrying once", streamErr.Error()),
						Reason:  "glm_stream_stall_retry",
					})
					continue
				}
				break
			}
		streamFinished:
			if streamErr != nil {
				if !errors.Is(streamErr, context.Canceled) {
					_, _ = l.emit(EventRunError, stepID, RunErrorPayload{Error: streamErr.Error()})
					l.emitErrorAssistance(ctx, stepID, streamErr)
				}
				return SolveResult{}, fmt.Errorf("provider stream: %w", streamErr)
			}
		} else {
			resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
				RunID:     l.Run.ID,
				StepID:    stepID,
				Messages:  msgs,
				Tools:     toolSpecs,
				Model:     l.Model,
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

		if err := l.Budget.AddTokens(usage.InputTokens, usage.OutputTokens); err != nil && terminalErr == nil {
			terminalErr = err
		}
		stepTokensUsed += usage.InputTokens + usage.OutputTokens
		stepOutputTokens += usage.OutputTokens
		if err := l.Budget.AddCost(usage.CostUSD); err != nil && terminalErr == nil {
			terminalErr = err
		}

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
			TTFT:       ttft,
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

		// ── Intervention Hooks ──────────────────────────────────────────
		if hres := l.runHooks(stepID, HookStageModelResponse); hres.Action != HookContinue {
			restartLoop, err := l.applyHookResult(hres, "system", &toolsStopped)
			if err != nil {
				return SolveResult{FinalText: finalText, Steps: 1}, err
			}
			if restartLoop {
				continue
			}
		}

		if terminalErr != nil {
			break
		}

		if toolsStopped {
			toolCalls = nil
		}
		if len(toolCalls) == 0 {
			break
		}

		denialLoopBreak := false
		denialStopTools := false
		postToolRestart := false
		for _, tc := range toolCalls {
			if isInspectionTool(tc.Name) {
				inspectionToolCalls++
			} else {
				inspectionOnly = false
			}
			if toolCallsUsed >= maxToolCalls {
				_, _ = l.emit(EventRunError, stepID, RunErrorPayload{
					Error: fmt.Sprintf("max tool calls per step reached (%d)", maxToolCalls),
				})
				break
			}
			denied, err := l.execToolCall(ctx, stepID, tc)
			if err != nil {
				return SolveResult{}, err
			}
			toolCallsUsed++
			if denied {
				key := tc.Name + ":" + string(tc.Args)
				denialCounts[key]++

				// Inject a steering message on the first denial to help the model pivot.
				if denialCounts[key] == 1 {
					msg := fmt.Sprintf("System: tool %q was denied. This tool is dangerous and requires operator approval. If you need this action, explain why to the user; otherwise, find a safe alternative.", tc.Name)
					l.Messages = append(l.Messages, providers.Message{
						Role:    "system",
						Content: msg,
					})
				}

				if denialCounts[key] >= 2 {
					msg := fmt.Sprintf("System: tool %q was denied %d times with the same arguments. Do not retry this action. Please summarize what you were trying to accomplish and stop.", tc.Name, denialCounts[key])
					_, _ = l.emit(EventHookIntervention, stepID, HookInterventionPayload{
						Action:  hookActionTraceName(HookStopTools),
						Message: msg,
						Reason:  "repeated_denial",
					})
					l.Messages = append(l.Messages, providers.Message{
						Role:    "system",
						Content: msg,
					})
					toolsStopped = true
					denialStopTools = true
					denialLoopBreak = true
					break
				}

				// Also break if we see too many denials of any kind in a single step to prevent spinning.
				totalDenials := 0
				for _, count := range denialCounts {
					totalDenials += count
				}
				if totalDenials >= 5 {
					msg := "System: too many tool denials in this step. Stop and ask the operator for guidance."
					_, _ = l.emit(EventHookIntervention, stepID, HookInterventionPayload{
						Action:  hookActionTraceName(HookStopTools),
						Message: msg,
						Reason:  "too_many_denials",
					})
					l.Messages = append(l.Messages, providers.Message{
						Role:    "system",
						Content: msg,
					})
					toolsStopped = true
					denialStopTools = true
					denialLoopBreak = true
					break
				}
			}
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
			if denialLoopBreak {
				break
			}
		}
		if denialLoopBreak {
			if denialStopTools {
				continue
			}
			break
		}
		if postToolRestart {
			continue
		}
		if toolCallsUsed >= maxToolCalls {
			break
		}
		stopToolsTriggered := false
		if !watchdogInjected && !watchdogsDisabled(l) {
			if msg, reason, action, ok := synthesisWatchdogMessage(toolCallsUsed, inspectionToolCalls, modelCalls, stepTokensUsed, inspectionOnly); ok {
				_, _ = l.emit(EventHookIntervention, stepID, HookInterventionPayload{
					Action:  hookActionTraceName(action),
					Message: msg,
					Reason:  reason,
				})
				if action == HookStopTools {
					l.Messages = append(l.Messages, providers.Message{
						Role:    "system",
						Content: msg,
					})
					toolsStopped = true
					watchdogInjected = true
					stopToolsTriggered = true
				} else {
					l.Messages = append(l.Messages, providers.Message{
						Role:    "system",
						Content: msg,
					})
					watchdogInjected = true
				}
			}
		}
		if stopToolsTriggered {
			continue
		}
	}

	// Emit step summary before incrementing step budget
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

	// Fix #5: Warn when token budget usage exceeds 50% and 80%
	if budgetAfter.MaxTokens > 0 {
		usagePercent := (budgetAfter.UsedTokens * 100) / budgetAfter.MaxTokens
		if usagePercent >= 80 && usagePercent < 100 {
			_, _ = l.emit(EventUserMsg, stepID, UserMsgPayload{
				Source:  "system",
				Content: fmt.Sprintf("⚠ System alert: token budget 80%% consumed (%d/%d tokens). Remaining budget is limited.", budgetAfter.UsedTokens, budgetAfter.MaxTokens),
			})
		} else if usagePercent >= 50 && usagePercent < 80 {
			_, _ = l.emit(EventUserMsg, stepID, UserMsgPayload{
				Source:  "system",
				Content: fmt.Sprintf("⚠ System alert: token budget 50%% consumed (%d/%d tokens). Plan remaining steps carefully.", budgetAfter.UsedTokens, budgetAfter.MaxTokens),
			})
		}
	}

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

func shouldWatchGLMStream(p providers.Provider) bool {
	return p != nil && strings.EqualFold(strings.TrimSpace(p.Name()), "glm")
}

func isInspectionTool(name string) bool {
	switch name {
	case "fs_read", "fs_list", "project_search", "fs_outline", "git_status", "git_diff", "inspect_tool", "blackboard_read", "blackboard_search":
		return true
	default:
		return false
	}
}

func synthesisWatchdogMessage(toolCallsUsed, inspectionToolCalls, modelCalls, stepTokensUsed int, inspectionOnly bool) (string, string, HookAction, bool) {
	if inspectionOnly &&
		toolCallsUsed >= inspectionWatchdogToolThreshold &&
		modelCalls >= inspectionWatchdogModelThreshold {
		return "System watchdog: you have spent too many tool calls on inspection-only exploration in this step. Tool use is now DISABLED for the remainder of this step. Stop exploring, synthesize what you already know, and provide your final answer.", "inspection_watchdog", HookStopTools, true
	}

	if modelCalls < readHeavyWatchdogModelThreshold ||
		stepTokensUsed < readHeavyWatchdogTokenThreshold ||
		inspectionToolCalls < readHeavyWatchdogToolThreshold {
		return "", "", HookContinue, false
	}
	if toolCallsUsed == 0 || inspectionToolCalls*5 < toolCallsUsed*4 {
		return "", "", HookContinue, false
	}
	return "System watchdog: this step is spending too many tokens on read-heavy inspection. Tool use is now DISABLED for the remainder of this step. Stop searching and reading, synthesize the evidence you already have, and answer now.", "read_heavy_watchdog", HookStopTools, true
}

func watchdogsDisabled(l *Loop) bool {
	return l != nil && l.Policy != nil && l.Policy.DisableWatchdogs
}

func hookActionTraceName(action HookAction) string {
	switch action {
	case HookInjectMessage:
		return "inject_message"
	case HookForceReplan:
		return "force_replan"
	case HookStopTools:
		return "stop_tools"
	case HookTerminate:
		return "terminate"
	default:
		return ""
	}
}

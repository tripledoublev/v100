package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

// ConfirmFn is called before executing a dangerous tool.
// Returns true if the user approved the action.
type ConfirmFn func(toolName, args string) bool

// OutputFn is called for each event emitted during the loop.
type OutputFn func(event Event)

// Loop is the main agent execution engine.
type Loop struct {
	Run       *Run
	Provider  providers.Provider
	Tools     *tools.Registry
	Policy    *policy.Policy
	Trace     *TraceWriter
	Budget    *BudgetTracker
	Messages  []providers.Message
	ConfirmFn ConfirmFn
	OutputFn  OutputFn
	GenParams providers.GenParams
	stepCount int // running step counter for step.summary events
}

// Step processes a single user input through the full model + tool execution cycle.
func (l *Loop) Step(ctx context.Context, userInput string) error {
	stepID := newID()
	stepStart := time.Now()
	budgetBefore := l.Budget.Budget()
	var modelCalls int

	// 1. Append user message
	userEv, err := l.emit(EventUserMsg, stepID, UserMsgPayload{Content: userInput})
	if err != nil {
		return err
	}
	_ = userEv
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
			return fmt.Errorf("provider: %w", err)
		}

		if err := l.Budget.AddTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens); err != nil {
			return err
		}
		if err := l.Budget.AddCost(resp.Usage.CostUSD); err != nil {
			return err
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
			return err
		}

		l.Messages = append(l.Messages, providers.Message{
			Role:      "assistant",
			Content:   resp.AssistantText,
			ToolCalls: resp.ToolCalls,
		})

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
				return err
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
		return err
	}

	return nil
}

// emitErrorAssistance tries one tool-free model turn to explain a failure and suggest remediation.
// If that fails, it emits a local fallback response so the transcript still guides the user.
func (l *Loop) emitErrorAssistance(ctx context.Context, stepID string, cause error) {
	msgs := append([]providers.Message{}, l.buildMessages()...)
	msgs = append(msgs, providers.Message{
		Role: "user",
		Content: "System error encountered while processing your request:\n" + cause.Error() +
			"\n\nPlease explain what likely happened and propose concrete next steps/remediation.",
	})

	resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    l.Run.ID,
		StepID:   stepID,
		Messages: msgs,
		Tools:    nil, // explanatory turn only; avoid cascading tool errors
		Model:    "",
	})
	if err != nil {
		fallback := "I hit an internal error and couldn't run a recovery explanation step.\n" +
			"Error: " + cause.Error() + "\n" +
			"Next steps: verify credentials/network, retry the command, and inspect the last tool result in the transcript."
		_, _ = l.emit(EventModelResp, stepID, ModelRespPayload{
			Text: fallback,
			Usage: Usage{
				InputTokens:  0,
				OutputTokens: 0,
				CostUSD:      0,
			},
		})
		l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: fallback})
		return
	}

	text := resp.AssistantText
	if strings.TrimSpace(text) == "" {
		text = "I hit an error but didn't receive additional diagnostic text. Please inspect the run.error and tool results."
	}
	_, _ = l.emit(EventModelResp, stepID, ModelRespPayload{
		Text:      text,
		ToolCalls: nil,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CostUSD:      resp.Usage.CostUSD,
		},
	})
	l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: text})
}

func (l *Loop) execToolCall(ctx context.Context, stepID string, tc providers.ToolCall) error {
	// Emit tool.call event
	_, err := l.emit(EventToolCall, stepID, ToolCallPayload{
		CallID: tc.ID,
		Name:   tc.Name,
		Args:   string(tc.Args),
	})
	if err != nil {
		return err
	}

	// Look up tool
	tool, ok := l.Tools.Get(tc.Name)
	if !ok {
		result := tools.ToolResult{OK: false, Output: fmt.Sprintf("tool %q not found or not enabled", tc.Name)}
		return l.emitToolResult(stepID, tc, result)
	}

	// Confirm dangerous tools
	if tool.DangerLevel() == tools.Dangerous {
		// Phase 1: Reflection turn for dangerous tools
		confidence, uncertainty, err := l.reflectOnTool(ctx, stepID, tc)
		if err == nil {
			_, _ = l.emit(EventToolReflect, stepID, ToolReflectPayload{
				CallID:      tc.ID,
				Name:        tc.Name,
				Confidence:  confidence,
				Uncertainty: uncertainty,
			})

			if confidence < 0.5 {
				msg := "low confidence rejection (conf=" + fmt.Sprintf("%.2f", confidence) + "): " + uncertainty
				result := tools.ToolResult{OK: false, Output: msg}
				if err := l.emitToolResult(stepID, tc, result); err != nil {
					return err
				}
				l.Messages = append(l.Messages, providers.Message{
					Role:       "tool",
					Content:    "ERROR: " + msg,
					ToolCallID: tc.ID,
					Name:       tc.Name,
				})
				return nil
			}
		}

		if l.ConfirmFn != nil && !l.ConfirmFn(tc.Name, string(tc.Args)) {
			result := tools.ToolResult{OK: false, Output: "user denied tool execution"}
			if err := l.emitToolResult(stepID, tc, result); err != nil {
				return err
			}
			// Add denial as tool message
			l.Messages = append(l.Messages, providers.Message{
				Role:       "tool",
				Content:    "user denied tool execution",
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
			return nil
		}
	}

	// Execute tool
	timeout := 30000
	if l.Policy != nil && l.Policy.ToolTimeoutMS > 0 {
		timeout = l.Policy.ToolTimeoutMS
	}
	callCtx := tools.ToolCallContext{
		RunID:        l.Run.ID,
		StepID:       stepID,
		CallID:       tc.ID,
		WorkspaceDir: l.Run.Dir,
		TimeoutMS:    timeout,
		Provider:     l.Provider,
	}

	result, err := tool.Exec(ctx, callCtx, tc.Args)
	if err != nil {
		result = tools.ToolResult{OK: false, Output: "tool exec error: " + err.Error()}
	}

	if err := l.emitToolResult(stepID, tc, result); err != nil {
		return err
	}

	// Add tool result to message history
	content := result.Output
	if !result.OK {
		content = "ERROR: " + result.Output
	}
	l.Messages = append(l.Messages, providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: tc.ID,
		Name:       tc.Name,
	})

	return nil
}

func (l *Loop) emitToolResult(stepID string, tc providers.ToolCall, result tools.ToolResult) error {
	_, err := l.emit(EventToolResult, stepID, ToolResultPayload{
		CallID:     tc.ID,
		Name:       tc.Name,
		OK:         result.OK,
		Output:     result.Output,
		DurationMS: result.DurationMS,
	})
	return err
}

func (l *Loop) reflectOnTool(ctx context.Context, stepID string, tc providers.ToolCall) (float64, string, error) {
	prompt := fmt.Sprintf("You are about to execute the tool %q with arguments: %s\n\n"+
		"On a scale of 0.0 to 1.0, what is your confidence that this is the correct next step to achieve the goal? "+
		"If below 0.7, please state your primary uncertainty concisely.\n\n"+
		"Respond ONLY in JSON format: {\"confidence\": 0.XX, \"uncertainty\": \"...\"}",
		tc.Name, string(tc.Args))

	msgs := append([]providers.Message{}, l.buildMessages()...)
	msgs = append(msgs, providers.Message{Role: "user", Content: prompt})

	resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    l.Run.ID,
		StepID:   stepID,
		Messages: msgs,
		Hints:    providers.Hints{JSONOnly: true},
	})
	if err != nil {
		return 0, "", err
	}

	var res struct {
		Confidence  float64 `json:"confidence"`
		Uncertainty string  `json:"uncertainty"`
	}
	if err := json.Unmarshal([]byte(resp.AssistantText), &res); err != nil {
		return 0.8, "failed to parse reflection", nil
	}

	return res.Confidence, res.Uncertainty, nil
}

func (l *Loop) buildMessages() []providers.Message {
	msgs := make([]providers.Message, 0, len(l.Messages)+2)

	// 1. Static system prompt
	if l.Policy != nil && l.Policy.SystemPrompt != "" {
		msgs = append(msgs, providers.Message{
			Role:    "system",
			Content: l.Policy.SystemPrompt,
		})
	}

	// 2. Dynamic persistent memory — re-read on every call so in-run writes are visible
	if l.Policy != nil && l.Policy.MemoryPath != "" {
		if mem, err := os.ReadFile(l.Policy.MemoryPath); err == nil && len(mem) > 0 {
			msgs = append(msgs, providers.Message{
				Role:    "system",
				Content: "## Persistent Memory\n\n" + string(mem),
			})
		}
	}

	// 3. Conversation history
	msgs = append(msgs, l.Messages...)
	return msgs
}

// estimateTokens returns an estimated token count for a message slice.
// Uses ~3.3 chars/token (more accurate than len/4 for mixed code/text) plus
// per-message framing overhead and tool call structure tokens.
func estimateTokens(msgs []providers.Message) int {
	n := 0
	for _, m := range msgs {
		n += 4 // per-message framing (role markers, separators)
		n += len(m.Content)*10/33 + 1
		for _, tc := range m.ToolCalls {
			n += 10 // tool call framing (id, name, type fields)
			n += len(tc.Args)*10/33 + 1
		}
	}
	return n
}

// maybeCompress compresses the oldest half of l.Messages when estimated tokens exceed
// 3/4 of the configured context limit, using a dedicated summarization call.
func (l *Loop) maybeCompress(ctx context.Context, stepID string) error {
	msgs := l.buildMessages()
	tokensBefore := estimateTokens(msgs)
	if tokensBefore < l.Policy.ContextLimit*3/4 {
		return nil
	}

	cutoff := len(l.Messages) / 2
	if cutoff < 4 {
		return nil // too short to compress meaningfully
	}
	msgsBefore := len(l.Messages)
	toSummarize := l.Messages[:cutoff]

	summaryReq := providers.CompleteRequest{
		RunID: l.Run.ID,
		Messages: append(
			[]providers.Message{{
				Role:    "system",
				Content: "You are a summarizer. Produce a dense, structured summary of the following conversation segment. Preserve: decisions made, files read/edited, tool results, current task state. Be concise.",
			}},
			toSummarize...,
		),
	}
	resp, err := l.Provider.Complete(ctx, summaryReq)
	if err != nil {
		return err
	}

	// Account for compression tokens against the budget.
	_ = l.Budget.AddTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens)
	_ = l.Budget.AddCost(resp.Usage.CostUSD)

	summary := providers.Message{
		Role:    "system",
		Content: "[CONTEXT SUMMARY — earlier conversation compressed]\n\n" + resp.AssistantText,
	}
	l.Messages = append([]providers.Message{summary}, l.Messages[cutoff:]...)

	tokensAfter := estimateTokens(l.buildMessages())
	_, _ = l.emit(EventCompress, stepID, CompressPayload{
		MessagesBefore: msgsBefore,
		MessagesAfter:  len(l.Messages),
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		CostUSD:        resp.Usage.CostUSD,
	})
	return nil
}

func (l *Loop) emit(t EventType, stepID string, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("emit marshal: %w", err)
	}
	ev := Event{
		TS:      time.Now().UTC(),
		RunID:   l.Run.ID,
		StepID:  stepID,
		EventID: newID(),
		Type:    t,
		Payload: b,
	}
	if err := l.Trace.Write(ev); err != nil {
		return ev, fmt.Errorf("trace write: %w", err)
	}
	if l.OutputFn != nil {
		l.OutputFn(ev)
	}
	return ev, nil
}

// EmitRunStart records the run.start event.
func (l *Loop) EmitRunStart(payload RunStartPayload) error {
	_, err := l.emit(EventRunStart, "", payload)
	return err
}

// EmitRunEnd records the run.end event.
func (l *Loop) EmitRunEnd(reason string) error {
	b := l.Budget.Budget()
	_, err := l.emit(EventRunEnd, "", RunEndPayload{
		Reason:     reason,
		UsedSteps:  b.UsedSteps,
		UsedTokens: b.UsedTokens,
	})
	return err
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
}

// Step processes a single user input through the full model + tool execution cycle.
func (l *Loop) Step(ctx context.Context, userInput string) error {
	stepID := newID()

	// 1. Append user message
	userEv, err := l.emit(EventUserMsg, stepID, UserMsgPayload{Content: userInput})
	if err != nil {
		return err
	}
	_ = userEv
	l.Messages = append(l.Messages, providers.Message{Role: "user", Content: userInput})

	// 2. Continue model/tool turns until the model produces a final (no-tool) response.
	maxToolCalls := 20
	if l.Policy != nil && l.Policy.MaxToolCallsPerStep > 0 {
		maxToolCalls = l.Policy.MaxToolCallsPerStep
	}
	toolCallsUsed := 0

	for {
		msgs := l.buildMessages()
		resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
			RunID:    l.Run.ID,
			StepID:   stepID,
			Messages: msgs,
			Tools:    l.Tools.Specs(),
			Model:    "",
			Hints: providers.Hints{
				MaxToolCalls: maxToolCalls - toolCallsUsed,
			},
		})
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

	// 7. Increment step budget
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

func (l *Loop) buildMessages() []providers.Message {
	msgs := make([]providers.Message, 0, len(l.Messages)+1)

	// System prompt
	if l.Policy != nil && l.Policy.SystemPrompt != "" {
		msgs = append(msgs, providers.Message{
			Role:    "system",
			Content: l.Policy.SystemPrompt,
		})
	}

	msgs = append(msgs, l.Messages...)
	return msgs
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

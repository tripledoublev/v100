package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

// PlanExecuteSolver implements a two-phase plan-then-execute strategy.
type PlanExecuteSolver struct {
	MaxReplans int
}

func (s *PlanExecuteSolver) Name() string { return "plan_execute" }

func (s *PlanExecuteSolver) Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error) {
	stepID := newID()
	budgetBefore := l.Budget.Budget()

	// 1. Initial Planning
	plan, err := s.plan(ctx, l, stepID, userInput)
	if err != nil {
		return SolveResult{}, err
	}

	// 2. Execution of the plan
	maxReplans := s.MaxReplans
	for replanCount := 0; replanCount <= maxReplans; replanCount++ {
		cp, err := l.CheckpointWithContext(ctx)
		if err != nil {
			return SolveResult{}, fmt.Errorf("checkpoint before executing plan: %w", err)
		}

		react := &ReactSolver{}
		res, err := react.Solve(ctx, l, "Please execute the next steps of the plan: "+plan)
		if err == nil {
			budgetAfter := l.Budget.Budget()
			return SolveResult{
				FinalText: res.FinalText,
				Steps:     res.Steps,
				Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
				CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
			}, nil
		}

		if replanCount == maxReplans {
			return res, err
		}
		if restoreErr := l.RestoreWithContext(ctx, cp); restoreErr != nil {
			return SolveResult{}, fmt.Errorf("restore checkpoint after execution failure: %w (original failure: %w)", restoreErr, err)
		}
		plan, err = s.replan(ctx, l, stepID, userInput, plan, err, replanCount+1)
		if err != nil {
			return SolveResult{}, err
		}
	}

	return SolveResult{}, fmt.Errorf("plan_execute: max replans reached")
}

func (s *PlanExecuteSolver) plan(ctx context.Context, l *Loop, stepID string, userInput string) (string, error) {
	prompt := fmt.Sprintf("TASK: %s\n\nPlease create a structured, numbered plan to solve this task. Do not execute any tools yet. Just provide the plan.", userInput)
	plan, err := s.generatePlan(ctx, l, stepID, prompt)
	if err != nil {
		return "", err
	}

	// Add the plan to the message history so subsequent steps see it.
	l.Messages = append(l.Messages, providers.Message{Role: "user", Content: userInput})
	l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: "Plan: " + plan})

	return plan, nil
}

func (s *PlanExecuteSolver) replan(ctx context.Context, l *Loop, stepID string, userInput, previousPlan string, execErr error, attempt int) (string, error) {
	prompt := fmt.Sprintf(
		"TASK: %s\n\nThe previous execution plan failed.\nPrevious plan:\n%s\n\nFailure:\n%s\n\nPlease create a revised structured, numbered plan. Do not execute tools yet.",
		userInput, previousPlan, execErr.Error(),
	)
	plan, err := s.generatePlan(ctx, l, stepID, prompt)
	if err != nil {
		return "", err
	}
	_, _ = l.emit(EventSolverReplan, stepID, SolverReplanPayload{
		Attempt: attempt,
		Error:   execErr.Error(),
		Plan:    plan,
	})
	l.Messages = append(l.Messages, providers.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("Revised Plan (attempt %d): %s", attempt, plan),
	})
	return plan, nil
}

func (s *PlanExecuteSolver) generatePlan(ctx context.Context, l *Loop, stepID, prompt string) (string, error) {
	msgs := append([]providers.Message{}, l.buildMessages()...)
	msgs = append(msgs, providers.Message{Role: "user", Content: prompt})

	var assistantText strings.Builder
	var usage providers.Usage
	t0 := time.Now()

	streamer, isStreamer := l.Provider.(providers.Streamer)
	if isStreamer && l.Policy != nil && l.Policy.Streaming {
		ch, err := streamer.StreamComplete(ctx, providers.CompleteRequest{
			RunID:    l.Run.ID,
			StepID:   stepID,
			Messages: msgs,
			Tools:    nil,
		})
		if err != nil {
			return "", err
		}
		for ev := range ch {
			switch ev.Type {
			case providers.StreamToken:
				assistantText.WriteString(ev.Text)
				_, _ = l.emit(EventModelToken, stepID, map[string]string{"text": ev.Text})
			case providers.StreamDone:
				usage = ev.Usage
			}
		}
	} else {
		resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
			RunID:    l.Run.ID,
			StepID:   stepID,
			Messages: msgs,
			Tools:    nil, // No tools in planning phase
		})
		if err != nil {
			return "", err
		}
		assistantText.WriteString(resp.AssistantText)
		usage = resp.Usage
	}

	durMS := time.Since(t0).Milliseconds()
	_ = l.Budget.AddTokens(usage.InputTokens, usage.OutputTokens)
	_ = l.Budget.AddCost(usage.CostUSD)

	_, _ = l.emit(EventModelResp, stepID, ModelRespPayload{
		Text: assistantText.String(),
		Usage: Usage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			CostUSD:      usage.CostUSD,
		},
		DurationMS: durMS,
	})

	plan := assistantText.String()
	_, _ = l.emit(EventSolverPlan, stepID, map[string]string{"plan": plan})
	return plan, nil
}

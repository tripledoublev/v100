package core

import (
	"context"
	"fmt"

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
	if maxReplans <= 0 {
		maxReplans = 3
	}

	for replanCount := 0; replanCount <= maxReplans; replanCount++ {
		// Use a reactive solver for the execution of the current plan/task
		// but with the plan in context.
		// For simplicity in this Phase 2a, we leverage the existing ReactSolver
		// but we inject the plan as a system message.
		
		react := &ReactSolver{}
		// We already appended the user message in the plan() call or we do it here.
		// Actually s.plan didn't append the user message to l.Messages permanently yet.
		
		res, err := react.Solve(ctx, l, "Please execute the next steps of the plan: "+plan)
		if err != nil {
			return res, err
		}

		// In a full implementation, we would check if the plan is complete or needs replanning.
		// For now, we'll return the result of the execution.
		budgetAfter := l.Budget.Budget()
		return SolveResult{
			FinalText: res.FinalText,
			Steps:     res.Steps,
			Tokens:    budgetAfter.UsedTokens - budgetBefore.UsedTokens,
			CostUSD:   budgetAfter.UsedCostUSD - budgetBefore.UsedCostUSD,
		}, nil
	}

	return SolveResult{}, fmt.Errorf("plan_execute: max replans reached")
}

func (s *PlanExecuteSolver) plan(ctx context.Context, l *Loop, stepID string, userInput string) (string, error) {
	prompt := fmt.Sprintf("TASK: %s\n\nPlease create a structured, numbered plan to solve this task. Do not execute any tools yet. Just provide the plan.", userInput)

	// We don't want to permanently append this planning prompt to history yet,
	// or maybe we do. Let's follow the spec: "Plan phase: Call model with tools=nil".
	
	msgs := append([]providers.Message{}, l.buildMessages()...)
	msgs = append(msgs, providers.Message{Role: "user", Content: prompt})

	resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    l.Run.ID,
		StepID:   stepID,
		Messages: msgs,
		Tools:    nil, // No tools in planning phase
	})
	if err != nil {
		return "", err
	}

	plan := resp.AssistantText
	_, _ = l.emit(EventSolverPlan, stepID, map[string]string{"plan": plan})

	// Add the plan to the message history so subsequent steps see it.
	l.Messages = append(l.Messages, providers.Message{Role: "user", Content: userInput})
	l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: "Plan: " + plan})

	return plan, nil
}

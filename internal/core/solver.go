package core

import (
	"context"
)

// SolveResult holds the outcome of a solver execution.
type SolveResult struct {
	FinalText string
	Steps     int
	Tokens    int
	CostUSD   float64
}

// Solver is the interface for different agent execution strategies.
type Solver interface {
	Name() string
	Solve(ctx context.Context, l *Loop, userInput string) (SolveResult, error)
}

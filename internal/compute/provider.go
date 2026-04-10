package compute

import (
	"context"
	"io"
)

// ExecuteRequest describes a single shell command to run on compute.
type ExecuteRequest struct {
	Command string
	WorkDir string
	Env     []string  // KEY=VALUE pairs; already includes RESEARCH_*, WANDB_*, custom vars
	Stdout  io.Writer // where to stream/collect output
	Stderr  io.Writer // may be the same writer as Stdout
}

// ExecuteResult is the outcome of one hook execution.
type ExecuteResult struct {
	ExitCode int
}

// Provider executes shell commands on a compute backend.
// All three research hooks (setup, command, collect) go through Execute.
type Provider interface {
	Name() string
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
}

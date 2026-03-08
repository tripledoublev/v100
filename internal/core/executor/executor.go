package executor

import (
	"context"
	"io"
)

// Result holds the outcome of a process execution.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// Session represents a persistent execution environment for a run.
type Session interface {
	ID() string
	Type() string // "host", "docker"
	
	// Start initializes the session (e.g. spawns container, prepares workspace).
	Start(ctx context.Context) error
	
	// Close terminates the session and cleans up resources.
	Close() error

	// Run executes a command within the session.
	Run(ctx context.Context, req RunRequest) (Result, error)

	// Workspace returns the host path to the sandbox workspace.
	Workspace() string
}

// RunRequest defines the parameters for executing a command.
type RunRequest struct {
	Command string
	Args    []string
	Env     []string
	Dir     string // directory relative to /workspace
	
	// Optional: Stream output deltas
	StdoutWriter io.Writer
	StderrWriter io.Writer
}

// Executor is a factory for creating sessions.
type Executor interface {
	NewSession(runID, sourceWorkspace string) (Session, error)
}

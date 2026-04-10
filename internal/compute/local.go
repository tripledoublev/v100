package compute

import (
	"context"
	"os/exec"
)

// LocalProvider runs commands on the local machine via sh -c.
// This is the default provider and preserves all existing behavior.
type LocalProvider struct{}

// NewLocalProvider creates a LocalProvider.
func NewLocalProvider() *LocalProvider { return &LocalProvider{} }

func (p *LocalProvider) Name() string { return "local" }

// Execute runs cmd via sh -c in the given working directory.
func (p *LocalProvider) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)
	cmd.Dir = req.WorkDir
	cmd.Env = req.Env
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	return ExecuteResult{ExitCode: code}, err
}

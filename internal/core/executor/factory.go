package executor

import (
	"fmt"

	"github.com/tripledoublev/v100/internal/config"
)

// NewExecutor creates an executor based on the provided configuration.
func NewExecutor(cfg config.SandboxConfig, baseDir string) (Executor, error) {
	if !cfg.Enabled {
		return &disabledExecutor{NewHostExecutor(baseDir)}, nil
	}

	switch cfg.Backend {
	case "host", "":
		return NewHostExecutor(baseDir), nil
	case "docker":
		return nil, fmt.Errorf("docker backend not yet implemented; use 'host' for Phase 3a foundation")
	default:
		return nil, fmt.Errorf("unknown sandbox backend %q", cfg.Backend)
	}
}

type disabledExecutor struct {
	h *HostExecutor
}

func (e *disabledExecutor) NewSession(runID, sourceWorkspace string) (Session, error) {
	s, err := e.h.NewSession(runID, sourceWorkspace)
	if err != nil {
		return nil, err
	}
	if hs, ok := s.(*HostSession); ok {
		hs.Enabled = false
	}
	return s, nil
}

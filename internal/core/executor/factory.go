package executor

import (
	"fmt"

	"github.com/tripledoublev/v100/internal/config"
)

// NewExecutor creates an executor based on the provided configuration.
func NewExecutor(cfg config.SandboxConfig, baseDir string) (Executor, error) {
	switch cfg.Backend {
	case "host", "":
		return NewHostExecutor(baseDir), nil
	case "docker":
		// Docker executor will be implemented in Phase 3d or later in Phase 3a.
		// For now return an error or a placeholder if Docker SDK is not yet linked.
		return nil, fmt.Errorf("docker backend not yet implemented in this foundation phase")
	default:
		return nil, fmt.Errorf("unknown sandbox backend %q", cfg.Backend)
	}
}

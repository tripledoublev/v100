package compute

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ModalConfig holds Modal-specific execution parameters.
type ModalConfig struct {
	GPU         string        // GPU type e.g. "A100", "T4", "A10G"
	Image       string        // container image override (optional)
	Timeout     time.Duration // injected as MODAL_TIMEOUT env var (default 30m)
	ModalSecret string        // named Modal secret to mount (e.g. "wandb-secret")
}

// ModalProvider runs commands via the Modal CLI.
// Modal handles remote GPU provisioning transparently — `modal run` executes
// locally but runs the function in a cloud container. setup and collect hooks
// still run as local shell commands (rsync/scp patterns still work).
type ModalProvider struct {
	cfg ModalConfig
}

// NewModalProvider creates a ModalProvider with the given config.
func NewModalProvider(cfg ModalConfig) *ModalProvider {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Minute
	}
	return &ModalProvider{cfg: cfg}
}

func (p *ModalProvider) Name() string { return "modal" }

// Execute runs cmd via sh -c with Modal-specific env vars injected.
// If ModalSecret is set and the command starts with "modal run", the secret
// is automatically mounted via --secret so WandB and other credentials reach
// the remote container without exposing them in the command string.
func (p *ModalProvider) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	env := append(req.Env,
		"MODAL_GPU="+p.cfg.GPU,
		"MODAL_IMAGE="+p.cfg.Image,
		fmt.Sprintf("MODAL_TIMEOUT=%d", int(p.cfg.Timeout.Seconds())),
	)

	command := req.Command
	if p.cfg.ModalSecret != "" && strings.HasPrefix(strings.TrimSpace(command), "modal run") {
		command = injectModalSecret(command, p.cfg.ModalSecret)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = req.WorkDir
	cmd.Env = env
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

// injectModalSecret inserts "--secret <name>" into a "modal run ..." command.
// e.g. "modal run foo.py" → "modal run --secret wandb-secret foo.py"
func injectModalSecret(command, secret string) string {
	trimmed := strings.TrimSpace(command)
	after, _ := strings.CutPrefix(trimmed, "modal run")
	return "modal run --secret " + secret + after
}

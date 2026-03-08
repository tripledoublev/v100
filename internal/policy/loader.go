package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tripledoublev/v100/internal/config"
)

// Load creates a Policy from a PolicyConfig entry.
// If the system prompt file doesn't exist, it falls back to the default prompt.
func Load(name string, cfg config.PolicyConfig) (*Policy, error) {
	p := &Policy{
		Name:                name,
		MaxToolCallsPerStep: cfg.MaxToolCallsPerStep,
		Streaming:           cfg.Streaming,
	}
	if p.MaxToolCallsPerStep == 0 {
		p.MaxToolCallsPerStep = 20
	}

	if cfg.SystemPromptPath != "" {
		promptPath := expandHome(cfg.SystemPromptPath)
		data, err := os.ReadFile(promptPath)
		if err == nil {
			p.SystemPrompt = string(data)
			return p, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("policy %s: read prompt %s: %w", name, promptPath, err)
		}
		// File doesn't exist — use default
	}

	p.SystemPrompt = DefaultSystemPrompt
	return p, nil
}

// Default returns a Policy with the built-in prompt.
func Default() *Policy {
	return &Policy{
		Name:                "default",
		SystemPrompt:        DefaultSystemPrompt,
		MaxToolCallsPerStep: 20,
		ToolTimeoutMS:       30000,
		Streaming:           true,
	}
}

// WriteDefaultPrompt writes the default system prompt to the XDG policy path.
func WriteDefaultPrompt() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "v100", "policies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "default.md")
	return os.WriteFile(path, []byte(DefaultSystemPrompt), 0o644)
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

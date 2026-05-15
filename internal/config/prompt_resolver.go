package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ResolvePrompt returns the resolved system prompt for an agent.
// If SystemPromptPath is set, it reads the file (resolved relative to baseDir if not absolute).
// Otherwise, falls back to inline SystemPrompt.
// Returns an error if the path is set but the file cannot be read.
func (a AgentConfig) ResolvePrompt(baseDir string) (string, error) {
	if strings.TrimSpace(a.SystemPromptPath) == "" {
		return a.SystemPrompt, nil
	}
	path := a.SystemPromptPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read system_prompt_path %q: %w", path, err)
	}
	return string(data), nil
}

// ResolvePrompt returns the resolved prompt for a wake task step.
// If PromptPath is set, it reads the file (resolved relative to baseDir if not absolute).
// Otherwise, falls back to inline Prompt.
func (s WakeTaskStep) ResolvePrompt(baseDir string) (string, error) {
	if strings.TrimSpace(s.PromptPath) == "" {
		return s.Prompt, nil
	}
	path := s.PromptPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt_path %q: %w", path, err)
	}
	return string(data), nil
}

// LoadAgentFile loads a standalone agent definition from a TOML file.
// Used for Phase 2 agent directories (~/.config/v100/agents/*.toml).
func LoadAgentFile(path string) (AgentConfig, error) {
	var agent AgentConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return agent, fmt.Errorf("read agent file %q: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &agent); err != nil {
		return agent, fmt.Errorf("parse agent file %q: %w", path, err)
	}
	return agent, nil
}

// LoadAgentsDirectory loads all agent definitions from a directory.
// Each .toml file in the directory is loaded as a separate agent,
// keyed by the filename (without extension).
// Returns an empty map (not an error) if the directory does not exist.
func LoadAgentsDirectory(dir string) (map[string]AgentConfig, error) {
	agents := map[string]AgentConfig{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return agents, nil
		}
		return nil, fmt.Errorf("read agents directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		key := strings.TrimSuffix(name, ".toml")
		agent, err := LoadAgentFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		agents[key] = agent
	}
	return agents, nil
}

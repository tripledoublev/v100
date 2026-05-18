package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// resolvePromptPath reads a prompt from the given path, applying ~ expansion
// and resolving relative paths against baseDir.
func resolvePromptPath(rawPath, baseDir, field string) (string, error) {
	path := expandHome(rawPath)
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(baseDir) == "" {
			baseDir = XDGConfigDir()
		}
		path = filepath.Join(baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s %q: %w", field, path, err)
	}
	return string(data), nil
}

// PromptBaseDir returns the directory relative prompt paths should resolve
// against for this config.
func (c *Config) PromptBaseDir() string {
	if c != nil && strings.TrimSpace(c.SourceDir) != "" {
		return c.SourceDir
	}
	return XDGConfigDir()
}

func configSourceDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return XDGConfigDir()
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Dir(path)
}

// ResolvePrompt returns the resolved system prompt for an agent.
// If SystemPromptPath is set, it reads the file. The path may use ~/ for
// home-relative paths and is otherwise resolved relative to baseDir.
// Otherwise, falls back to inline SystemPrompt.
// Returns an error if the path is set but the file cannot be read.
func (a AgentConfig) ResolvePrompt(baseDir string) (string, error) {
	if strings.TrimSpace(a.SystemPromptPath) == "" {
		return a.SystemPrompt, nil
	}
	return resolvePromptPath(a.SystemPromptPath, baseDir, "system_prompt_path")
}

// ResolvePrompt returns the resolved prompt for a wake task step.
// If PromptPath is set, it reads the file. The path may use ~/ for
// home-relative paths and is otherwise resolved relative to baseDir.
// Otherwise, falls back to inline Prompt.
func (s WakeTaskStep) ResolvePrompt(baseDir string) (string, error) {
	if strings.TrimSpace(s.PromptPath) == "" {
		return s.Prompt, nil
	}
	return resolvePromptPath(s.PromptPath, baseDir, "prompt_path")
}

// XDGConfigDir returns the directory containing the user's v100 config file.
// Used as the default baseDir for resolving prompt_path / system_prompt_path
// when callers do not supply one explicitly.
func XDGConfigDir() string {
	return filepath.Dir(XDGConfigPath())
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

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	taskManifestName   = "task.toml"
	policyManifestName = "policy.toml"
)

// LoadBehaviorDirectories merges standalone behavioral definitions from
// agents/, tasks/, and policies/ directories beside the active config file.
func LoadBehaviorDirectories(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	baseDir := cfg.PromptBaseDir()
	if err := loadAgentDefinitions(cfg, filepath.Join(baseDir, "agents")); err != nil {
		return err
	}
	if err := loadTaskDefinitions(cfg, filepath.Join(baseDir, "tasks")); err != nil {
		return err
	}
	if err := loadPolicyDefinitions(cfg, filepath.Join(baseDir, "policies")); err != nil {
		return err
	}
	return nil
}

func loadAgentDefinitions(cfg *Config, dir string) error {
	agents, err := LoadAgentsDirectory(dir)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return nil
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentConfig{}
	}
	for _, name := range sortedMapKeys(agents) {
		cfg.Agents[name] = agents[name]
	}
	return nil
}

func loadTaskDefinitions(cfg *Config, dir string) error {
	tasks, err := LoadTasksDirectory(dir)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	cfg.Wake.Tasks = mergeWakeTasks(cfg.Wake.Tasks, tasks)
	return nil
}

func loadPolicyDefinitions(cfg *Config, dir string) error {
	policies, err := LoadPoliciesDirectory(dir)
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	if cfg.Policies == nil {
		cfg.Policies = map[string]PolicyConfig{}
	}
	for _, name := range sortedMapKeys(policies) {
		cfg.Policies[name] = policies[name]
	}
	return nil
}

func mergeWakeTasks(existing []WakeTask, loaded map[string]WakeTask) []WakeTask {
	if len(loaded) == 0 {
		return existing
	}
	out := append([]WakeTask(nil), existing...)
	index := map[string]int{}
	for i, task := range out {
		name := strings.TrimSpace(task.Name)
		if name != "" {
			index[name] = i
		}
	}
	for _, name := range sortedMapKeys(loaded) {
		task := loaded[name]
		if i, ok := index[name]; ok {
			out[i] = task
			continue
		}
		index[name] = len(out)
		out = append(out, task)
	}
	return out
}

// LoadTaskFile loads a standalone wake task from a TOML file.
func LoadTaskFile(path string) (WakeTask, error) {
	defaultName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return loadTaskFile(path, defaultName)
}

func loadTaskFile(path, defaultName string) (WakeTask, error) {
	var task WakeTask
	data, err := os.ReadFile(path)
	if err != nil {
		return task, fmt.Errorf("read task file %q: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &task); err != nil {
		return task, fmt.Errorf("parse task file %q: %w", path, err)
	}
	task.SourceDir = configSourceDir(path)
	if strings.TrimSpace(task.Name) == "" {
		task.Name = defaultName
	}
	return task, nil
}

// LoadTasksDirectory loads standalone wake tasks from tasks/*.toml and
// tasks/<name>/task.toml. Directory task manifests resolve prompt_path entries
// relative to the task directory.
func LoadTasksDirectory(dir string) (map[string]WakeTask, error) {
	tasks := map[string]WakeTask{}
	entries, err := sortedDirEntries(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return tasks, nil
		}
		return nil, fmt.Errorf("read tasks directory %q: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		switch {
		case entry.IsDir():
			manifestPath := filepath.Join(path, taskManifestName)
			if _, err := os.Stat(manifestPath); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("stat task manifest %q: %w", manifestPath, err)
			}
			task, err := loadTaskFile(manifestPath, name)
			if err != nil {
				return nil, err
			}
			if err := addTask(tasks, task); err != nil {
				return nil, err
			}
		case strings.HasSuffix(name, ".toml") && name != taskManifestName:
			task, err := LoadTaskFile(path)
			if err != nil {
				return nil, err
			}
			if err := addTask(tasks, task); err != nil {
				return nil, err
			}
		}
	}
	return tasks, nil
}

func addTask(tasks map[string]WakeTask, task WakeTask) error {
	name := strings.TrimSpace(task.Name)
	if name == "" {
		return fmt.Errorf("task definition in %q has no name", task.SourceDir)
	}
	if _, exists := tasks[name]; exists {
		return fmt.Errorf("duplicate task definition %q", name)
	}
	tasks[name] = task
	return nil
}

// LoadPolicyFile loads a standalone policy definition from a TOML file.
func LoadPolicyFile(path string) (PolicyConfig, error) {
	var pc PolicyConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return pc, fmt.Errorf("read policy file %q: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &pc); err != nil {
		return pc, fmt.Errorf("parse policy file %q: %w", path, err)
	}
	pc.SourceDir = configSourceDir(path)
	return pc, nil
}

// LoadPoliciesDirectory loads policies from policies/*.toml, policies/*.md,
// policies/*.txt, and policies/<name>/policy.toml.
func LoadPoliciesDirectory(dir string) (map[string]PolicyConfig, error) {
	policies := map[string]PolicyConfig{}
	entries, err := sortedDirEntries(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return policies, nil
		}
		return nil, fmt.Errorf("read policies directory %q: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		switch {
		case entry.IsDir():
			manifestPath := filepath.Join(path, policyManifestName)
			if _, err := os.Stat(manifestPath); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("stat policy manifest %q: %w", manifestPath, err)
			}
			pc, err := LoadPolicyFile(manifestPath)
			if err != nil {
				return nil, err
			}
			if err := addPolicy(policies, name, pc); err != nil {
				return nil, err
			}
		case strings.HasSuffix(name, ".toml") && name != policyManifestName:
			pc, err := LoadPolicyFile(path)
			if err != nil {
				return nil, err
			}
			key := strings.TrimSuffix(name, ".toml")
			if err := addPolicy(policies, key, pc); err != nil {
				return nil, err
			}
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".txt") {
			key := strings.TrimSuffix(name, filepath.Ext(name))
			if _, exists := policies[key]; exists {
				continue
			}
			pc := PolicyConfig{SourceDir: dir, SystemPromptPath: name}
			if err := addPolicy(policies, key, pc); err != nil {
				return nil, err
			}
		}
	}
	return policies, nil
}

func addPolicy(policies map[string]PolicyConfig, name string, pc PolicyConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("policy definition has no name")
	}
	if _, exists := policies[name]; exists {
		return fmt.Errorf("duplicate policy definition %q", name)
	}
	policies[name] = pc
	return nil
}

func sortedDirEntries(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

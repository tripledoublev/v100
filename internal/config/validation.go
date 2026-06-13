package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

type ValidationSeverity string

const (
	ValidationError   ValidationSeverity = "error"
	ValidationWarning ValidationSeverity = "warning"
	ValidationInfo    ValidationSeverity = "info"
)

type ValidationFinding struct {
	Severity ValidationSeverity
	Path     string
	Message  string
}

type ValidationResult struct {
	ConfigPath string
	Findings   []ValidationFinding
}

func (r *ValidationResult) Add(severity ValidationSeverity, path, message string) {
	r.Findings = append(r.Findings, ValidationFinding{
		Severity: severity,
		Path:     path,
		Message:  message,
	})
}

func (r *ValidationResult) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == ValidationError {
			return true
		}
	}
	return false
}

func (r *ValidationResult) Counts() (errors, warnings, info int) {
	for _, f := range r.Findings {
		switch f.Severity {
		case ValidationError:
			errors++
		case ValidationWarning:
			warnings++
		case ValidationInfo:
			info++
		}
	}
	return errors, warnings, info
}

var deprecatedConfigKeys = map[string]string{
	"defaults.max_tool_calls": "defaults.max_tool_calls_per_step",
	"tools.allow":             "tools.enabled",
	"tools.deny":              "tools.dangerous",
}

// ValidateConfigPath validates config.toml and the behavior directories beside it.
func ValidateConfigPath(path string) *ValidationResult {
	path = expandHome(path)
	result := &ValidationResult{ConfigPath: path}
	data, err := os.ReadFile(path)
	if err != nil {
		result.Add(ValidationError, path, fmt.Sprintf("read config: %v", err))
		return result
	}

	var raw Config
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		result.Add(ValidationError, path, fmt.Sprintf("TOML syntax error in config: %v", err))
		return result
	}
	warnUndecodedKeys(result, path, md.Undecoded())
	raw.SourceDir = configSourceDir(path)

	validateBehaviorDirectories(result, raw.PromptBaseDir())
	if result.HasErrors() {
		return result
	}

	cfg, err := loadConfigFile(path)
	if err != nil {
		result.Add(ValidationError, path, fmt.Sprintf("load effective config: %v", err))
		return result
	}
	validateEffectiveConfig(result, cfg)
	return result
}

func warnUndecodedKeys(result *ValidationResult, path string, keys []toml.Key) {
	for _, key := range keys {
		name := key.String()
		if replacement, ok := deprecatedConfigKeys[name]; ok {
			result.Add(ValidationWarning, path, fmt.Sprintf("deprecated config key %q; use %q", name, replacement))
			continue
		}
		result.Add(ValidationWarning, path, fmt.Sprintf("unused config key or section %q", name))
	}
}

func validateBehaviorDirectories(result *ValidationResult, baseDir string) {
	validateAgentsDir(result, filepath.Join(baseDir, "agents"))
	validatePoliciesDir(result, filepath.Join(baseDir, "policies"))
	validateTasksDir(result, filepath.Join(baseDir, "tasks"))
}

func validateAgentsDir(result *ValidationResult, dir string) {
	entries, err := sortedDirEntries(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Add(ValidationInfo, dir, "behavior dir agents/ not present")
			return
		}
		result.Add(ValidationError, dir, fmt.Sprintf("read agents directory: %v", err))
		return
	}
	seen := map[string]string{}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		count++
		path := filepath.Join(dir, entry.Name())
		name := strings.TrimSuffix(entry.Name(), ".toml")
		if prev, ok := seen[name]; ok {
			result.Add(ValidationError, path, fmt.Sprintf("duplicate agent definition %q also defined in %s", name, prev))
			continue
		}
		seen[name] = path
		var agent AgentConfig
		md, err := decodeTOMLFile(path, &agent)
		if err != nil {
			result.Add(ValidationError, path, fmt.Sprintf("TOML syntax error in agent definition: %v", err))
			continue
		}
		warnUndecodedKeys(result, path, md.Undecoded())
		validatePromptPath(result, path, agent.SystemPromptPath, configSourceDir(path), "system_prompt_path")
	}
	result.Add(ValidationInfo, dir, fmt.Sprintf("behavior dir agents/: %d agent definition(s)", count))
}

func validatePoliciesDir(result *ValidationResult, dir string) {
	entries, err := sortedDirEntries(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Add(ValidationInfo, dir, "behavior dir policies/ not present")
			return
		}
		result.Add(ValidationError, dir, fmt.Sprintf("read policies directory: %v", err))
		return
	}
	seen := map[string]string{}
	count := 0
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
				result.Add(ValidationError, manifestPath, fmt.Sprintf("stat policy manifest: %v", err))
				continue
			}
			count += validatePolicyTOML(result, manifestPath, name, seen)
		case strings.HasSuffix(name, ".toml") && name != policyManifestName:
			key := strings.TrimSuffix(name, ".toml")
			count += validatePolicyTOML(result, path, key, seen)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
			continue
		}
		key := strings.TrimSuffix(name, filepath.Ext(name))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = filepath.Join(dir, name)
		count++
	}
	result.Add(ValidationInfo, dir, fmt.Sprintf("behavior dir policies/: %d policy definition(s)", count))
}

func validatePolicyTOML(result *ValidationResult, path, name string, seen map[string]string) int {
	if prev, ok := seen[name]; ok {
		result.Add(ValidationError, path, fmt.Sprintf("duplicate policy definition %q also defined in %s", name, prev))
		return 0
	}
	seen[name] = path
	var policy PolicyConfig
	md, err := decodeTOMLFile(path, &policy)
	if err != nil {
		result.Add(ValidationError, path, fmt.Sprintf("TOML syntax error in policy definition: %v", err))
		return 1
	}
	warnUndecodedKeys(result, path, md.Undecoded())
	validatePromptPath(result, path, policy.SystemPromptPath, configSourceDir(path), "system_prompt_path")
	return 1
}

func validateTasksDir(result *ValidationResult, dir string) {
	entries, err := sortedDirEntries(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Add(ValidationInfo, dir, "behavior dir tasks/ not present")
			return
		}
		result.Add(ValidationError, dir, fmt.Sprintf("read tasks directory: %v", err))
		return
	}
	seen := map[string]string{}
	count := 0
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
				result.Add(ValidationError, manifestPath, fmt.Sprintf("stat task manifest: %v", err))
				continue
			}
			count += validateTaskTOML(result, manifestPath, name, seen)
		case strings.HasSuffix(name, ".toml") && name != taskManifestName:
			defaultName := strings.TrimSuffix(name, ".toml")
			count += validateTaskTOML(result, path, defaultName, seen)
		}
	}
	result.Add(ValidationInfo, dir, fmt.Sprintf("behavior dir tasks/: %d task definition(s)", count))
}

func validateTaskTOML(result *ValidationResult, path, defaultName string, seen map[string]string) int {
	var task WakeTask
	md, err := decodeTOMLFile(path, &task)
	if err != nil {
		result.Add(ValidationError, path, fmt.Sprintf("TOML syntax error in task definition: %v", err))
		return 1
	}
	warnUndecodedKeys(result, path, md.Undecoded())
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = defaultName
	}
	if prev, ok := seen[name]; ok {
		result.Add(ValidationError, path, fmt.Sprintf("duplicate task definition %q also defined in %s", name, prev))
		return 1
	}
	seen[name] = path
	baseDir := configSourceDir(path)
	for i, step := range task.Steps {
		validatePromptPath(result, path, step.PromptPath, baseDir, fmt.Sprintf("steps[%d].prompt_path", i))
	}
	return 1
}

func validatePromptPath(result *ValidationResult, ownerPath, rawPath, baseDir, field string) {
	if strings.TrimSpace(rawPath) == "" {
		return
	}
	resolved := ResolvePromptFilePath(rawPath, baseDir)
	if _, err := os.Stat(resolved); err != nil {
		result.Add(ValidationError, ownerPath, fmt.Sprintf("%s references missing file %q: %v", field, resolved, err))
	}
}

func validateEffectiveConfig(result *ValidationResult, cfg *Config) {
	if cfg == nil {
		return
	}
	checkProviderReference(result, cfg, "defaults.provider", cfg.Defaults.Provider, true)
	checkProviderReference(result, cfg, "defaults.smart_provider", cfg.Defaults.SmartProvider, false)
	checkProviderReference(result, cfg, "defaults.sub_provider", cfg.Defaults.SubProvider, false)
	checkProviderReference(result, cfg, "defaults.cheap_provider", cfg.Defaults.CheapProvider, false)
	checkProviderReference(result, cfg, "defaults.compress_provider", cfg.Defaults.CompressProvider, false)
	checkProviderReference(result, cfg, "wake.provider", cfg.Wake.Provider, false)
	checkProviderReference(result, cfg, "embedding.provider", cfg.Embedding.Provider, false)

	for providerName, provider := range cfg.Providers {
		for _, fallback := range provider.Fallbacks {
			checkProviderReference(result, cfg, "providers."+providerName+".fallbacks", fallback, true)
		}
	}
	validateProviderFallbackCycles(result, cfg)
	validateWakeTaskReference(result, cfg)
	validateToolCredentialConfig(result, cfg)
	validateUIConfig(result, cfg)
}

func checkProviderReference(result *ValidationResult, cfg *Config, field, name string, required bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		if required {
			result.Add(ValidationError, field, "provider reference is empty")
		}
		return
	}
	if _, ok := cfg.Providers[name]; !ok {
		result.Add(ValidationError, field, fmt.Sprintf("references provider %q, but no [providers.%s] config exists", name, name))
	}
}

func validateWakeTaskReference(result *ValidationResult, cfg *Config) {
	name := strings.TrimSpace(cfg.Wake.Task)
	if name == "" {
		return
	}
	for _, task := range cfg.Wake.Tasks {
		if task.Name == name {
			return
		}
	}
	result.Add(ValidationError, "wake.task", fmt.Sprintf("references task %q, but no matching [[wake.tasks]] or tasks/ definition exists", name))
}

func validateProviderFallbackCycles(result *ValidationResult, cfg *Config) {
	graph := map[string][]string{}
	for name, provider := range cfg.Providers {
		for _, fallback := range provider.Fallbacks {
			if _, ok := cfg.Providers[fallback]; ok {
				graph[name] = append(graph[name], fallback)
			}
		}
		sort.Strings(graph[name])
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var visit func(string) bool
	visit = func(name string) bool {
		if visiting[name] {
			cycle := append(stack, name)
			result.Add(ValidationError, "providers."+name+".fallbacks", "provider fallback cycle: "+strings.Join(cycle, " -> "))
			return true
		}
		if visited[name] {
			return false
		}
		visiting[name] = true
		stack = append(stack, name)
		for _, fallback := range graph[name] {
			if visit(fallback) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		visiting[name] = false
		visited[name] = true
		return false
	}
	for _, name := range sortedMapKeys(graph) {
		if visit(name) {
			return
		}
	}
}

func validateToolCredentialConfig(result *ValidationResult, cfg *Config) {
	for i, name := range cfg.Tools.Env.Allow {
		if !validEnvName(strings.TrimSpace(name)) {
			result.Add(ValidationError, fmt.Sprintf("tools.env.allow[%d]", i), fmt.Sprintf("invalid environment variable name %q", name))
		}
	}
	gh := cfg.Tools.Auth.GitHub
	switch strings.ToLower(strings.TrimSpace(gh.Mode)) {
	case "", "disabled", "env":
	default:
		result.Add(ValidationError, "tools.auth.github.mode", fmt.Sprintf("unsupported GitHub auth mode %q; use disabled or env", gh.Mode))
	}
	if strings.EqualFold(strings.TrimSpace(gh.Mode), "env") && !validEnvName(strings.TrimSpace(gh.Env)) {
		result.Add(ValidationError, "tools.auth.github.env", fmt.Sprintf("invalid GitHub token environment variable name %q", gh.Env))
	}
}

func validateUIConfig(result *ValidationResult, cfg *Config) {
	theme := strings.ToLower(strings.TrimSpace(cfg.UI.Theme))
	if theme == "" || theme == "default" {
		return
	}
	switch theme {
	case "v100", "mono", "dracula", "catppuccin":
	default:
		result.Add(ValidationError, "ui.theme", fmt.Sprintf("unsupported UI theme %q; use v100, mono, dracula, or catppuccin", cfg.UI.Theme))
	}
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 && !(r == '_' || unicode.IsLetter(r)) {
			return false
		}
		if !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func decodeTOMLFile(path string, dst any) (toml.MetaData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toml.MetaData{}, fmt.Errorf("read %s: %w", path, err)
	}
	md, err := toml.Decode(string(data), dst)
	if err != nil {
		return toml.MetaData{}, err
	}
	return md, nil
}

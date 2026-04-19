package ui

import (
	"strings"

	"github.com/tripledoublev/v100/internal/config"
)

func ResolveReviewTargets(cfg *config.Config) ReviewTargets {
	return ReviewTargets{
		Codex:  resolveReviewTarget(cfg, "codex", "codex"),
		Claude: resolveReviewTarget(cfg, "anthropic", "claude"),
	}
}

func resolveReviewTarget(cfg *config.Config, providerType string, label string) reviewTargetConfig {
	if cfg == nil {
		return reviewTargetConfig{}
	}
	if name, pc, ok := findProviderByType(cfg, providerType); ok && strings.TrimSpace(pc.DefaultModel) != "" {
		return reviewTargetConfig{
			Enabled:      true,
			Label:        label,
			ProviderName: name,
			ModelName:    pc.DefaultModel,
		}
	}
	return reviewTargetConfig{Label: label}
}

func findProviderByType(cfg *config.Config, providerType string) (string, config.ProviderConfig, bool) {
	if cfg == nil {
		return "", config.ProviderConfig{}, false
	}
	preferred := map[string][]string{
		"codex":     {"codex"},
		"anthropic": {"anthropic", "claude"},
	}
	for _, name := range preferred[providerType] {
		if pc, ok := cfg.Providers[name]; ok && strings.EqualFold(pc.Type, providerType) {
			return name, pc, true
		}
	}
	for name, pc := range cfg.Providers {
		if strings.EqualFold(pc.Type, providerType) {
			return name, pc, true
		}
	}
	return "", config.ProviderConfig{}, false
}

func classifyProviderFamily(provider, model string) string {
	combined := strings.ToLower(strings.TrimSpace(provider + " " + model))
	switch {
	case strings.Contains(combined, "codex"), strings.Contains(combined, "gpt-"), strings.Contains(combined, "openai"):
		return "codex"
	case strings.Contains(combined, "claude"), strings.Contains(combined, "anthropic"):
		return "claude"
	default:
		return "other"
	}
}

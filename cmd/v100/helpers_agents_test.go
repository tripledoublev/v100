package main

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestConfiguredAgentNamesSorted(t *testing.T) {
	cfg := &config.Config{
		Agents: map[string]config.AgentConfig{
			"reviewer": {},
			"coder":    {},
			"research": {},
		},
	}

	got := configuredAgentNames(cfg)
	want := []string{"coder", "research", "reviewer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("configuredAgentNames() = %v, want %v", got, want)
	}
}

func TestFormatUnknownAgentRoleIncludesGuidance(t *testing.T) {
	msg := formatUnknownAgentRole(config.DefaultConfig(), "default")
	for _, want := range []string{
		"unknown agent role: default",
		"available: coder, researcher, reviewer",
		"v100 agents",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("formatUnknownAgentRole() missing %q in %q", want, msg)
		}
	}
}

func TestAgentsCmdListsDefaultRoles(t *testing.T) {
	cfgPath := t.TempDir() + "/missing.toml"

	out, err := captureStdout(func() error {
		cmd := agentsCmd(&cfgPath)
		return cmd.RunE(cmd, nil)
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"coder", "researcher", "reviewer"} {
		if !strings.Contains(out, want) {
			t.Fatalf("agents output missing %q in:\n%s", want, out)
		}
	}
}

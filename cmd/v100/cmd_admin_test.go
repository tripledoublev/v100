package main

import (
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestSandboxBackendNeedsDocker(t *testing.T) {
	cfg := config.DefaultConfig()
	if sandboxBackendNeedsDocker(cfg) {
		t.Fatal("expected host backend to not require docker")
	}
	cfg.Sandbox.Backend = "docker"
	if !sandboxBackendNeedsDocker(cfg) {
		t.Fatal("expected docker backend to require docker")
	}
}

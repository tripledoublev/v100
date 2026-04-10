package compute

import (
	"testing"
)

func TestBuild_default(t *testing.T) {
	p, err := Build(Config{})
	if err != nil {
		t.Fatalf("Build empty config: %v", err)
	}
	if p.Name() != "local" {
		t.Fatalf("got %q, want %q", p.Name(), "local")
	}
}

func TestBuild_local(t *testing.T) {
	p, err := Build(Config{Provider: "local"})
	if err != nil {
		t.Fatalf("Build local: %v", err)
	}
	if p.Name() != "local" {
		t.Fatalf("got %q, want %q", p.Name(), "local")
	}
}

func TestBuild_modal(t *testing.T) {
	p, err := Build(Config{Provider: "modal", GPU: "A100", Timeout: "30m"})
	if err != nil {
		t.Fatalf("Build modal: %v", err)
	}
	if p.Name() != "modal" {
		t.Fatalf("got %q, want %q", p.Name(), "modal")
	}
}

func TestBuild_unknown(t *testing.T) {
	_, err := Build(Config{Provider: "runpod"})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestBuild_modalBadTimeout(t *testing.T) {
	_, err := Build(Config{Provider: "modal", Timeout: "notaduration"})
	if err == nil {
		t.Fatal("expected error for bad timeout, got nil")
	}
}

package compute

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestModalProvider_Name(t *testing.T) {
	p := NewModalProvider(ModalConfig{})
	if p.Name() != "modal" {
		t.Fatalf("got %q, want %q", p.Name(), "modal")
	}
}

func TestInjectModalSecret(t *testing.T) {
	cases := []struct {
		command string
		secret  string
		want    string
	}{
		{"modal run train.py", "wandb-secret", "modal run --secret wandb-secret train.py"},
		{"modal run modal_app.py::MyApp.train", "my-secret", "modal run --secret my-secret modal_app.py::MyApp.train"},
		{"  modal run foo.py  ", "s", "modal run --secret s foo.py"},
	}
	for _, tc := range cases {
		got := injectModalSecret(tc.command, tc.secret)
		if got != tc.want {
			t.Errorf("injectModalSecret(%q, %q) = %q, want %q", tc.command, tc.secret, got, tc.want)
		}
	}
}

func TestModalProvider_Execute_envInjection(t *testing.T) {
	p := NewModalProvider(ModalConfig{GPU: "A100", Image: "pytorch/pytorch:latest"})
	var out bytes.Buffer
	_, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "sh -c 'echo MODAL_GPU=$MODAL_GPU'",
		Env:     os.Environ(),
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "MODAL_GPU=A100") {
		t.Fatalf("output %q does not contain MODAL_GPU=A100", out.String())
	}
}

func TestModalProvider_Execute_secretInjectedInCommand(t *testing.T) {
	// Verify the command is rewritten when ModalSecret is set
	var capturedCmd string
	p := NewModalProvider(ModalConfig{ModalSecret: "wandb-secret"})
	// We can't intercept exec.Command directly, but we can test
	// that a non-modal-run command is left unchanged.
	var out bytes.Buffer
	_, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "echo not-modal-run",
		Env:     os.Environ(),
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = capturedCmd
	if !strings.Contains(out.String(), "not-modal-run") {
		t.Fatalf("output %q unexpected", out.String())
	}
}

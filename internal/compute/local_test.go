package compute

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestLocalProvider_Name(t *testing.T) {
	p := NewLocalProvider()
	if p.Name() != "local" {
		t.Fatalf("got %q, want %q", p.Name(), "local")
	}
}

func TestLocalProvider_Execute_success(t *testing.T) {
	p := NewLocalProvider()
	var out bytes.Buffer
	res, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "echo hello",
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("output %q does not contain 'hello'", out.String())
	}
}

func TestLocalProvider_Execute_nonzero(t *testing.T) {
	p := NewLocalProvider()
	var out bytes.Buffer
	_, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "exit 2",
		Stdout:  &out,
		Stderr:  &out,
	})
	if err == nil {
		t.Fatal("expected error for exit 2, got nil")
	}
}

func TestLocalProvider_Execute_envPassthrough(t *testing.T) {
	p := NewLocalProvider()
	var out bytes.Buffer
	res, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "sh -c 'echo $MY_TEST_VAR'",
		Env:     append(os.Environ(), "MY_TEST_VAR=xyzzy"),
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(out.String(), "xyzzy") {
		t.Fatalf("output %q does not contain env var value", out.String())
	}
}

func TestLocalProvider_Execute_workdir(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider()
	var out bytes.Buffer
	_, err := p.Execute(context.Background(), ExecuteRequest{
		Command: "pwd",
		WorkDir: dir,
		Stdout:  &out,
		Stderr:  &out,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), dir) {
		t.Fatalf("output %q does not contain workdir %q", out.String(), dir)
	}
}

package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/tools"
)

type recordingSession struct {
	calls int
}

func TestShouldVerifyBuildAfterShellReadOnlyCommand(t *testing.T) {
	args, err := json.Marshal(map[string]string{"cmd": "seq 1 12000"})
	if err != nil {
		t.Fatal(err)
	}
	if shouldVerifyBuildAfterTool(tools.Sh(), args) {
		t.Fatal("read-only shell command should not trigger build verification")
	}
}

func TestShouldVerifyBuildAfterShellMutatingCommand(t *testing.T) {
	args, err := json.Marshal(map[string]string{"cmd": "printf hi > note.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !shouldVerifyBuildAfterTool(tools.Sh(), args) {
		t.Fatal("mutating shell command should trigger build verification")
	}
}

func (s *recordingSession) ID() string                  { return "recording" }
func (s *recordingSession) Type() string                { return "host" }
func (s *recordingSession) Start(context.Context) error { return nil }
func (s *recordingSession) Close() error                { return nil }
func (s *recordingSession) Workspace() string           { return "" }
func (s *recordingSession) Run(_ context.Context, _ executor.RunRequest) (executor.Result, error) {
	s.calls++
	return executor.Result{ExitCode: 0}, nil
}

func TestVerifyBuildUsesActiveSession(t *testing.T) {
	runDir := t.TempDir()
	reg := tools.NewRegistry([]string{"sh"})
	reg.Register(tools.Sh())
	session := &recordingSession{}

	loop := &Loop{
		Run:     &Run{ID: "verify-build-test", Dir: runDir, TraceFile: filepath.Join(runDir, "trace.jsonl")},
		Tools:   reg,
		Session: session,
		Mapper:  NewPathMapper(runDir, runDir),
	}

	if err := loop.verifyBuild(context.Background(), "step-1"); err != nil {
		t.Fatalf("verifyBuild() error = %v", err)
	}
	if session.calls != 1 {
		t.Fatalf("session calls = %d, want 1", session.calls)
	}
	for _, msg := range loop.Messages {
		if msg.Role == "system" {
			t.Fatalf("unexpected system alert injected on successful build: %q", msg.Content)
		}
	}
}

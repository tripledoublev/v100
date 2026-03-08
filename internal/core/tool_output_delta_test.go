package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

func TestLoopEmitsToolOutputDeltaEvents(t *testing.T) {
	sourceDir := t.TempDir()
	factory := executor.NewHostExecutor(t.TempDir())
	session, err := factory.NewSession("run-1", sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	sandboxDir := session.Workspace()
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{
						ID:   "call-1",
						Name: "sh",
						Args: json.RawMessage(`{"cmd":"printf '%s' \"$PWD/file.txt\"; printf '%s' problem >&2"}`),
					},
				},
			},
			{AssistantText: "done"},
		},
	}

	loop, trace := newTestLoop(t, prov, []string{"sh"})
	defer func() { _ = trace.Close() }()
	loop.Tools.Register(tools.Sh())
	loop.Session = session
	loop.Mapper = core.NewPathMapper(sourceDir, sandboxDir)
	loop.Run.Dir = sandboxDir

	if err := loop.Step(context.Background(), "stream output"); err != nil {
		t.Fatal(err)
	}

	events, err := core.ReadAll(trace.Path())
	if err != nil {
		t.Fatal(err)
	}

	var stdoutSeen, stderrSeen bool
	for _, ev := range events {
		if ev.Type != core.EventToolOutputDelta {
			continue
		}
		var p core.ToolOutputDeltaPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		switch p.Stream {
		case "stdout":
			stdoutSeen = true
			if p.Name != "sh" || p.CallID != "call-1" {
				t.Fatalf("unexpected stdout delta metadata: %+v", p)
			}
			if p.Delta != "/workspace/file.txt" {
				t.Fatalf("stdout delta = %q, want sanitized /workspace path", p.Delta)
			}
		case "stderr":
			stderrSeen = true
			if p.Delta != "problem" {
				t.Fatalf("stderr delta = %q, want problem", p.Delta)
			}
		}
	}
	if !stdoutSeen {
		t.Fatal("expected stdout tool.output_delta event")
	}
	if !stderrSeen {
		t.Fatal("expected stderr tool.output_delta event")
	}
}

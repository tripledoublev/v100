package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockStreamer struct {
	MockProvider
	events []providers.StreamEvent
}

func (m *mockStreamer) StreamComplete(ctx context.Context, req providers.CompleteRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (m *mockStreamer) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true, Streaming: true}
}

func (m *mockStreamer) Metadata(_ context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: "mock", ContextSize: 4096}, nil
}

func TestReactSolverStreaming(t *testing.T) {
	ctx := context.Background()
	events := []providers.StreamEvent{
		{Type: providers.StreamToken, Text: "I will "},
		{Type: providers.StreamToken, Text: "list files."},
		{Type: providers.StreamToolCallStart, ToolCallID: "tc1", ToolCallName: "fs_list"},
		{Type: providers.StreamToolCallDelta, ToolCallID: "tc1", ToolCallArgs: `{"path":`},
		{Type: providers.StreamToolCallDelta, ToolCallID: "tc1", ToolCallArgs: ` "."}`},
		{Type: providers.StreamDone, Usage: providers.Usage{InputTokens: 10, OutputTokens: 5}},
	}

	p := &mockStreamer{
		events: events,
	}

	reg := tools.NewRegistry([]string{"fs_list"})
	reg.Register(tools.FSList())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	pol := &policy.Policy{Streaming: true}

	l := &Loop{
		Run:      &Run{ID: "test-stream", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 10}),
		Solver:   &ReactSolver{},
		Policy:   pol,
		Mapper:   NewPathMapper(runDir, runDir),
	}

	_, err := l.Solver.Solve(ctx, l, "List files")
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}

	// Verify trace for streaming events
	trEvents, _ := ReadAll(trace.Path())
	hasToken := false
	hasTCDelta := false
	for _, ev := range trEvents {
		if ev.Type == EventModelToken {
			hasToken = true
		}
		if ev.Type == EventToolCallDelta {
			hasTCDelta = true
		}
	}
	if !hasToken {
		t.Error("expected model.token in trace")
	}
	if !hasTCDelta {
		t.Error("expected tool.call_delta in trace")
	}
}

// Regression: MiniMax (and other Anthropic-compatible providers) sometimes
// emit tool calls as raw <minimax:tool_call>…</…> XML inside streamed text
// tokens. The solver must strip that markup from the final assistant text
// and promote the invocation into a real ToolCall before emitting
// EventModelResp — otherwise the XML leaks into the TUI transcript pane.
func TestReactSolverStreaming_StripsTextualToolCallXML(t *testing.T) {
	ctx := context.Background()
	xml := "<minimax:tool_call>\n<invoke name=\"fs_list\">\n<parameter name=\"path\">.</parameter>\n</invoke>\n</minimax:tool_call>"
	events := []providers.StreamEvent{
		{Type: providers.StreamToken, Text: "let me check\n"},
		{Type: providers.StreamToken, Text: xml},
		{Type: providers.StreamDone, Usage: providers.Usage{InputTokens: 10, OutputTokens: 5}},
	}

	p := &mockStreamer{events: events}

	reg := tools.NewRegistry([]string{"fs_list"})
	reg.Register(tools.FSList())

	runDir := t.TempDir()
	trace, _ := OpenTrace(runDir + "/trace.jsonl")
	defer func() { _ = trace.Close() }()

	pol := &policy.Policy{Streaming: true}

	l := &Loop{
		Run:      &Run{ID: "test-stream-xml", Dir: runDir},
		Provider: p,
		Tools:    reg,
		Trace:    trace,
		Budget:   NewBudgetTracker(&Budget{MaxSteps: 1}),
		Solver:   &ReactSolver{},
		Policy:   pol,
		Mapper:   NewPathMapper(runDir, runDir),
	}

	_, _ = l.Solver.Solve(ctx, l, "list files")

	trEvents, _ := ReadAll(trace.Path())
	var payload ModelRespPayload
	found := false
	for _, ev := range trEvents {
		if ev.Type == EventModelResp {
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("decode ModelRespPayload: %v", err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no EventModelResp in trace")
	}
	if strings.Contains(payload.Text, "<invoke") || strings.Contains(payload.Text, "<minimax:tool_call>") {
		t.Errorf("assistant text leaks XML markup: %q", payload.Text)
	}
	if !strings.Contains(payload.Text, "let me check") {
		t.Errorf("assistant text lost surrounding prose: %q", payload.Text)
	}
	if len(payload.ToolCalls) != 1 || payload.ToolCalls[0].Name != "fs_list" {
		t.Fatalf("expected extracted fs_list tool call, got %+v", payload.ToolCalls)
	}
}

package core_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockNetworkTool struct {
	calls int
}

func (t *mockNetworkTool) Name() string        { return "mock_network" }
func (t *mockNetworkTool) Description() string { return "Mock network tool for loop tests." }
func (t *mockNetworkTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *mockNetworkTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)
}
func (t *mockNetworkTool) DangerLevel() tools.DangerLevel { return tools.Safe }
func (t *mockNetworkTool) Effects() tools.ToolEffects {
	return tools.ToolEffects{NeedsNetwork: true}
}
func (t *mockNetworkTool) Exec(_ context.Context, _ tools.ToolCallContext, _ json.RawMessage) (tools.ToolResult, error) {
	t.calls++
	return tools.ToolResult{OK: true, Output: "network ok"}, nil
}

func TestLoopBlocksNetworkToolWhenNetworkTierOff(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "mock_network", Args: json.RawMessage(`{}`)},
				},
			},
			{AssistantText: "done"},
		},
	}

	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()

	netTool := &mockNetworkTool{}
	reg := tools.NewRegistry([]string{"mock_network"})
	reg.Register(netTool)

	loop := &core.Loop{
		Run:         &core.Run{ID: "network-off", Dir: dir, TraceFile: tracePath},
		Provider:    prov,
		Tools:       reg,
		Policy:      policy.Default(),
		Trace:       trace,
		Budget:      core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
		ConfirmFn:   func(_, _ string) bool { return true },
		Mapper:      core.NewPathMapper(dir, dir),
		NetworkTier: "off",
	}

	if err := loop.Step(context.Background(), "try network"); err != nil {
		t.Fatal(err)
	}
	if netTool.calls != 0 {
		t.Fatalf("network tool executed %d times, want 0", netTool.calls)
	}

	events, err := core.ReadAll(trace.Path())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != core.EventToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Name != "mock_network" {
			continue
		}
		found = true
		if payload.OK {
			t.Fatal("expected blocked network tool result to be !OK")
		}
		if !strings.Contains(payload.Output, "network access is disabled by sandbox policy") {
			t.Fatalf("unexpected tool result output: %q", payload.Output)
		}
	}
	if !found {
		t.Fatal("expected tool.result event for blocked network tool")
	}
}

func TestLoopAllowsNetworkToolWhenNetworkTierResearch(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "mock_network", Args: json.RawMessage(`{}`)},
				},
			},
			{AssistantText: "done"},
		},
	}

	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()

	netTool := &mockNetworkTool{}
	reg := tools.NewRegistry([]string{"mock_network"})
	reg.Register(netTool)

	loop := &core.Loop{
		Run:         &core.Run{ID: "network-on", Dir: dir, TraceFile: tracePath},
		Provider:    prov,
		Tools:       reg,
		Policy:      policy.Default(),
		Trace:       trace,
		Budget:      core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
		ConfirmFn:   func(_, _ string) bool { return true },
		Mapper:      core.NewPathMapper(dir, dir),
		NetworkTier: "research",
	}

	if err := loop.Step(context.Background(), "try network"); err != nil {
		t.Fatal(err)
	}
	if netTool.calls != 1 {
		t.Fatalf("network tool executed %d times, want 1", netTool.calls)
	}
}

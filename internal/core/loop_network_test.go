package core_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type mockSession struct {
	sessionType string
	runCalls    int
}

func (s *mockSession) ID() string                    { return "mock-session" }
func (s *mockSession) Type() string                  { return s.sessionType }
func (s *mockSession) Start(_ context.Context) error { return nil }
func (s *mockSession) Close() error                  { return nil }
func (s *mockSession) Workspace() string             { return "" }
func (s *mockSession) Run(_ context.Context, req executor.RunRequest) (executor.Result, error) {
	s.runCalls++
	return executor.Result{ExitCode: 0, Stdout: "ok\n"}, nil
}

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
	defer func() { _ = trace.Close() }()

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
	defer func() { _ = trace.Close() }()

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

func TestLoopAllowsShellToolInDockerWhenNetworkTierOff(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "sh", Args: json.RawMessage(`{"cmd":"printf ok"}`)},
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
	defer func() { _ = trace.Close() }()

	session := &mockSession{sessionType: "docker"}
	reg := tools.NewRegistry([]string{"sh"})
	reg.Register(tools.Sh())

	loop := &core.Loop{
		Run:         &core.Run{ID: "docker-shell-off", Dir: dir, TraceFile: tracePath},
		Provider:    prov,
		Tools:       reg,
		Policy:      policy.Default(),
		Trace:       trace,
		Budget:      core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
		ConfirmFn:   func(_, _ string) bool { return true },
		Mapper:      core.NewPathMapper(dir, dir),
		NetworkTier: "off",
		Session:     session,
	}

	if err := loop.Step(context.Background(), "run local shell"); err != nil {
		t.Fatal(err)
	}
	if session.runCalls != 1 {
		t.Fatalf("shell tool run calls = %d, want 1", session.runCalls)
	}
}

func TestLoopBlocksShellToolInHostWhenNetworkTierOff(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.CompleteResponse{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "sh", Args: json.RawMessage(`{"cmd":"printf ok"}`)},
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
	defer func() { _ = trace.Close() }()

	session := &mockSession{sessionType: "host"}
	reg := tools.NewRegistry([]string{"sh"})
	reg.Register(tools.Sh())

	loop := &core.Loop{
		Run:         &core.Run{ID: "host-shell-off", Dir: dir, TraceFile: tracePath},
		Provider:    prov,
		Tools:       reg,
		Policy:      policy.Default(),
		Trace:       trace,
		Budget:      core.NewBudgetTracker(&core.Budget{MaxSteps: 10}),
		ConfirmFn:   func(_, _ string) bool { return true },
		Mapper:      core.NewPathMapper(dir, dir),
		NetworkTier: "off",
		Session:     session,
	}

	if err := loop.Step(context.Background(), "run local shell"); err != nil {
		t.Fatal(err)
	}
	if session.runCalls != 0 {
		t.Fatalf("shell tool run calls = %d, want 0", session.runCalls)
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
		if payload.Name != "sh" {
			continue
		}
		found = true
		if payload.OK {
			t.Fatal("expected blocked host shell tool result to be !OK")
		}
		if !strings.Contains(payload.Output, "network access is disabled by sandbox policy") {
			t.Fatalf("unexpected tool result output: %q", payload.Output)
		}
	}
	if !found {
		t.Fatal("expected tool.result event for blocked host shell tool")
	}
}

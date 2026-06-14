package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateStructuredHandoffStandardSchema(t *testing.T) {
	schema, name, err := ResolveHandoffSchema("standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	if name != HandoffSchemaStandard {
		t.Fatalf("schema name = %q, want %q", name, HandoffSchemaStandard)
	}

	raw, diagnostics := ValidateStructuredHandoff("```json\n{\"status\":\"ok\",\"summary\":\"done\",\"next_steps\":[\"ship\"]}\n```", schema)
	if len(diagnostics) > 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"summary":"done"`) {
		t.Fatalf("validated raw = %s", raw)
	}

	_, diagnostics = ValidateStructuredHandoff(`{"status":"maybe","summary":"done"}`, schema)
	if len(diagnostics) == 0 {
		t.Fatal("expected diagnostics for invalid standard handoff")
	}
	got := strings.Join(diagnostics, "\n")
	for _, want := range []string{"$.next_steps is required", "$.status must be one of"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics missing %q: %#v", want, diagnostics)
		}
	}
}

func TestValidateStructuredHandoffCustomSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["answer"],
		"properties":{"answer":{"type":"string"},"score":{"type":"integer"}}
	}`)
	resolved, name, err := ResolveHandoffSchema("", schema)
	if err != nil {
		t.Fatal(err)
	}
	if name != "custom" {
		t.Fatalf("schema name = %q, want custom", name)
	}
	_, diagnostics := ValidateStructuredHandoff(`{"answer":"yes","score":1.5}`, resolved)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "$.score must be integer") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestAgentToolReturnsStructuredPayload(t *testing.T) {
	var got AgentRunParams
	tool := NewAgent(func(_ context.Context, params AgentRunParams) AgentRunResult {
		got = params
		return AgentRunResult{
			OK:         true,
			AgentRunID: "agent-call-1",
			Result:     "human handoff",
			Structured: json.RawMessage(`{"status":"ok","summary":"done","next_steps":["ship"]}`),
		}
	})
	args := json.RawMessage(`{"task":"check","handoff_schema_name":"standard"}`)
	res, err := tool.Exec(context.Background(), ToolCallContext{CallID: "call-1"}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("tool failed: %s", res.Output)
	}
	if got.HandoffSchemaName != "standard" {
		t.Fatalf("handoff schema name = %q, want standard", got.HandoffSchemaName)
	}
	if strings.Contains(res.Output, "json=") {
		t.Fatalf("human output should not embed json= payload: %s", res.Output)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Structured, &payload); err != nil {
		t.Fatalf("structured payload: %v", err)
	}
	if payload["agent_run_id"] != "agent-call-1" {
		t.Fatalf("payload = %#v", payload)
	}
	handoff, ok := payload["handoff"].(map[string]any)
	if !ok || handoff["summary"] != "done" {
		t.Fatalf("handoff payload = %#v", payload["handoff"])
	}
}

func TestDispatchToolReturnsStructuredPayloadWithoutEmbeddedJSON(t *testing.T) {
	tool := NewDispatch(func(_ context.Context, params AgentRunParams) AgentRunResult {
		if params.HandoffSchemaName != "standard" {
			t.Fatalf("handoff schema name = %q, want standard", params.HandoffSchemaName)
		}
		return AgentRunResult{
			OK:         true,
			AgentRunID: "agent-call-1",
			Result:     "review done",
			Structured: json.RawMessage(`{"status":"ok","summary":"reviewed","next_steps":["merge"]}`),
		}
	}, func() []string { return []string{"reviewer"} })
	res, err := tool.Exec(context.Background(), ToolCallContext{CallID: "call-1"}, json.RawMessage(`{
		"agent":"reviewer",
		"task":"review",
		"handoff_schema_name":"standard"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "json=") {
		t.Fatalf("dispatch output should not embed json= payload: %s", res.Output)
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Structured, &payload); err != nil {
		t.Fatalf("structured payload: %v", err)
	}
	if payload["agent"] != "reviewer" || payload["agent_run_id"] != "agent-call-1" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestOrchestrateToolReturnsStructuredPayloadWithoutEmbeddedJSON(t *testing.T) {
	tool := NewOrchestrate(func(_ context.Context, params AgentRunParams) AgentRunResult {
		return AgentRunResult{
			OK:         true,
			AgentRunID: "agent-" + params.CallID,
			Result:     "done",
			Structured: json.RawMessage(`{"status":"ok","summary":"done","next_steps":["ship"]}`),
		}
	}, func() []string { return []string{"reviewer"} })
	res, err := tool.Exec(context.Background(), ToolCallContext{CallID: "call-1"}, json.RawMessage(`{
		"pattern":"fanout",
		"tasks":[{"agent":"reviewer","task":"review","handoff_schema_name":"standard"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "json=") {
		t.Fatalf("orchestrate output should not embed json= payload: %s", res.Output)
	}
	var payload struct {
		OK      bool `json:"ok"`
		Results []struct {
			Agent   string         `json:"agent"`
			Handoff map[string]any `json:"handoff"`
		} `json:"results"`
	}
	if err := json.Unmarshal(res.Structured, &payload); err != nil {
		t.Fatalf("structured payload: %v", err)
	}
	if !payload.OK || len(payload.Results) != 1 || payload.Results[0].Agent != "reviewer" || payload.Results[0].Handoff["summary"] != "done" {
		t.Fatalf("payload = %#v", payload)
	}
}

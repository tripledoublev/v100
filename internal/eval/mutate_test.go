package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
)

func TestMutatePolicyNoIssues(t *testing.T) {
	// Trace with no failures should return original policy unchanged.
	userPayload, _ := json.Marshal(core.UserMsgPayload{Content: "hello"})
	modelPayload, _ := json.Marshal(core.ModelRespPayload{
		Text:  "response",
		Usage: core.Usage{InputTokens: 10, OutputTokens: 5},
	})
	toolCallPayload, _ := json.Marshal(core.ToolCallPayload{CallID: "c1", Name: "fs_list", Args: `{"path":"."}`})
	toolResultPayload, _ := json.Marshal(core.ToolResultPayload{CallID: "c1", Name: "fs_list", OK: true, Output: "file.go"})
	step1, _ := json.Marshal(core.StepSummaryPayload{StepNumber: 1, InputTokens: 10, OutputTokens: 5, ToolCalls: 1, ModelCalls: 1})
	step2, _ := json.Marshal(core.StepSummaryPayload{StepNumber: 2, InputTokens: 10, OutputTokens: 5, ToolCalls: 1, ModelCalls: 1})
	endPayload, _ := json.Marshal(core.RunEndPayload{Reason: "completed", UsedSteps: 2, UsedTokens: 30})

	events := []core.Event{
		{Type: core.EventUserMsg, Payload: userPayload},
		{Type: core.EventModelResp, Payload: modelPayload},
		{Type: core.EventToolCall, Payload: toolCallPayload},
		{Type: core.EventToolResult, Payload: toolResultPayload},
		{Type: core.EventStepSummary, Payload: step1},
		{Type: core.EventStepSummary, Payload: step2},
		{Type: core.EventRunEnd, Payload: endPayload},
	}

	policy := "You are a helpful agent."
	result, err := eval.MutatePolicy(context.Background(), nil, "", policy, events)
	if err != nil {
		t.Fatal(err)
	}
	if result.MutatedPolicy != policy {
		t.Errorf("expected unchanged policy, got %q", result.MutatedPolicy)
	}
	if result.OriginalPolicy != policy {
		t.Errorf("expected original policy preserved")
	}
}

func TestPolicyMutationResultFields(t *testing.T) {
	var r eval.PolicyMutationResult
	r.OriginalPolicy = "original"
	r.MutatedPolicy = "mutated"
	r.Rationale = "test reason"

	if r.OriginalPolicy != "original" || r.MutatedPolicy != "mutated" || r.Rationale != "test reason" {
		t.Error("PolicyMutationResult fields not set correctly")
	}
}

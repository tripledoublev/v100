package eval_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
	"github.com/tripledoublev/v100/internal/providers"
)

type stubProvider struct {
	text string
}

func (p stubProvider) Name() string                         { return "stub" }
func (p stubProvider) Capabilities() providers.Capabilities { return providers.Capabilities{} }
func (p stubProvider) Complete(context.Context, providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{AssistantText: p.text}, nil
}
func (p stubProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (p stubProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

func TestMutatePolicyNoIssues(t *testing.T) {
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
	result, err := eval.MutatePolicy(context.Background(), nil, "", eval.MutationBudgets{}, policy, events)
	if err != nil {
		t.Fatal(err)
	}
	if result.MutatedPolicy != policy {
		t.Errorf("expected unchanged policy, got %q", result.MutatedPolicy)
	}
	if result.CandidatePolicy != policy {
		t.Errorf("expected candidate policy to stay unchanged, got %q", result.CandidatePolicy)
	}
	if result.OriginalPolicy != policy {
		t.Errorf("expected original policy preserved")
	}
	if result.RejectedReason != "" {
		t.Fatalf("unexpected rejection: %s", result.RejectedReason)
	}
}

func TestMutatePromptRejectsOverBudgetGrowth(t *testing.T) {
	prompt := "list files"
	candidate := prompt + strings.Repeat(" please", 20)
	result, err := eval.MutatePrompt(context.Background(), stubProvider{text: "MUTATED PROMPT: " + candidate + "\nRATIONALE: add more detail"}, "", eval.MutationBudgets{MaxPromptChars: 1000, MaxPromptGrowthChars: 10}, failingEvents(prompt))
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidatePrompt != candidate {
		t.Fatalf("CandidatePrompt = %q, want %q", result.CandidatePrompt, candidate)
	}
	if result.MutatedPrompt != prompt {
		t.Fatalf("MutatedPrompt = %q, want original %q after rejection", result.MutatedPrompt, prompt)
	}
	if !strings.Contains(result.RejectedReason, "mutated prompt exceeds max growth") {
		t.Fatalf("RejectedReason = %q, want growth rejection", result.RejectedReason)
	}
}

func TestMutatePolicyRejectsOverBudgetGrowth(t *testing.T) {
	policy := "You are a careful agent."
	candidate := policy + strings.Repeat(" Add another rule.", 20)
	result, err := eval.MutatePolicy(context.Background(), stubProvider{text: "MUTATED POLICY: " + candidate + "\nRATIONALE: add more rules"}, "", eval.MutationBudgets{MaxPromptChars: 1000, MaxPromptGrowthChars: 20}, policy, failingEvents("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidatePolicy != candidate {
		t.Fatalf("CandidatePolicy = %q, want %q", result.CandidatePolicy, candidate)
	}
	if result.MutatedPolicy != policy {
		t.Fatalf("MutatedPolicy = %q, want original %q after rejection", result.MutatedPolicy, policy)
	}
	if !strings.Contains(result.RejectedReason, "mutated policy exceeds max growth") {
		t.Fatalf("RejectedReason = %q, want growth rejection", result.RejectedReason)
	}
}

func TestPolicyMutationResultFields(t *testing.T) {
	var r eval.PolicyMutationResult
	r.OriginalPolicy = "original"
	r.CandidatePolicy = "candidate"
	r.MutatedPolicy = "mutated"
	r.Rationale = "test reason"
	r.RejectedReason = "too long"

	if r.OriginalPolicy != "original" || r.CandidatePolicy != "candidate" || r.MutatedPolicy != "mutated" || r.Rationale != "test reason" || r.RejectedReason != "too long" {
		t.Error("PolicyMutationResult fields not set correctly")
	}
}

func failingEvents(prompt string) []core.Event {
	userPayload, _ := json.Marshal(core.UserMsgPayload{Content: prompt})
	modelPayload, _ := json.Marshal(core.ModelRespPayload{
		Text:  "response",
		Usage: core.Usage{InputTokens: 12, OutputTokens: 6},
	})
	toolResultPayload, _ := json.Marshal(core.ToolResultPayload{CallID: "c1", Name: "missing_tool", OK: false, Output: "not found or not enabled"})
	endPayload, _ := json.Marshal(core.RunEndPayload{Reason: "error", UsedSteps: 1, UsedTokens: 18})
	return []core.Event{
		{Type: core.EventUserMsg, Payload: userPayload},
		{Type: core.EventModelResp, Payload: modelPayload},
		{Type: core.EventToolResult, Payload: toolResultPayload},
		{Type: core.EventRunEnd, Payload: endPayload},
	}
}

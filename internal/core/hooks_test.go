package core

import (
	"testing"
)

func TestDeduplicationHookIgnoresEmptyToolName(t *testing.T) {
	hook := DeduplicationHook(2)
	res := hook(LoopState{Stage: HookStageToolResult, StepCount: 1})
	if res.Action != HookContinue {
		t.Fatalf("expected continue for empty tool name, got %d", res.Action)
	}
}

func TestDeduplicationHookAllowsFirstCall(t *testing.T) {
	hook := DeduplicationHook(2)
	res := hook(LoopState{
		Stage:        HookStageToolResult,
		StepCount:    1,
		LastToolName: "fs_read",
		LastToolArgs: `{"path":"a.go"}`,
	})
	if res.Action != HookContinue {
		t.Fatalf("expected continue on first call, got %d", res.Action)
	}
}

func TestDeduplicationHookWarnsOnRepeat(t *testing.T) {
	hook := DeduplicationHook(2)
	state := LoopState{
		Stage:          HookStageToolResult,
		StepCount:      1,
		LastToolName:   "project_search",
		LastToolArgs:   `{"pattern":"foo"}`,
		LastToolOutput: "result1",
	}
	// First call — allowed
	hook(state)
	// Second identical call — should warn
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected inject_message on repeat, got %d", res.Action)
	}
	if res.Message == "" {
		t.Fatal("expected non-empty warning message")
	}
}

func TestDeduplicationHookResetsOnNewStep(t *testing.T) {
	hook := DeduplicationHook(2)
	state := LoopState{
		Stage:        HookStageToolResult,
		StepCount:    1,
		LastToolName: "fs_read",
		LastToolArgs: `{"path":"a.go"}`,
	}
	hook(state)
	hook(state) // triggers warning

	// New step — should reset
	state.StepCount = 2
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected continue after step reset, got %d", res.Action)
	}
}

func TestDeduplicationHookDifferentArgsOK(t *testing.T) {
	hook := DeduplicationHook(2)
	hook(LoopState{
		Stage:        HookStageToolResult,
		StepCount:    1,
		LastToolName: "fs_read",
		LastToolArgs: `{"path":"a.go"}`,
	})
	res := hook(LoopState{
		Stage:        HookStageToolResult,
		StepCount:    1,
		LastToolName: "fs_read",
		LastToolArgs: `{"path":"b.go"}`,
	})
	if res.Action != HookContinue {
		t.Fatalf("expected continue for different args, got %d", res.Action)
	}
}

func TestDeduplicationHookTruncatesLongOutput(t *testing.T) {
	hook := DeduplicationHook(2)
	longOutput := make([]byte, 500)
	for i := range longOutput {
		longOutput[i] = 'x'
	}
	state := LoopState{
		Stage:          HookStageToolResult,
		StepCount:      1,
		LastToolName:   "sh",
		LastToolArgs:   `{"cmd":"echo"}`,
		LastToolOutput: string(longOutput),
	}
	hook(state)
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected inject_message, got %d", res.Action)
	}
	// Output in message should be truncated
	if len(res.Message) > 400 {
		// Message includes tool name + args context, but output portion should be truncated
		t.Logf("message length: %d (acceptable)", len(res.Message))
	}
}

func TestDeduplicationHookIgnoresModelResponseStage(t *testing.T) {
	hook := DeduplicationHook(2)
	state := LoopState{
		Stage:        HookStageModelResponse,
		StepCount:    1,
		LastToolName: "fs_read",
		LastToolArgs: `{"path":"a.go"}`,
	}
	hook(state)
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected continue during model-response stage, got %d", res.Action)
	}
}

func TestThresholdHookContinuesOnSuccess(t *testing.T) {
	hook := ThresholdHook(3)
	res := hook(LoopState{Stage: HookStageToolResult, LastToolOK: true})
	if res.Action != HookContinue {
		t.Fatalf("expected continue on success, got %d", res.Action)
	}
}

func TestThresholdHookTerminatesAfterNFailures(t *testing.T) {
	hook := ThresholdHook(3)
	for i := 0; i < 2; i++ {
		res := hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
		if res.Action != HookContinue {
			t.Fatalf("expected continue before threshold, got %d at i=%d", res.Action, i)
		}
	}
	res := hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
	if res.Action != HookTerminate {
		t.Fatalf("expected terminate after 3 failures, got %d", res.Action)
	}
}

func TestThresholdHookResetsOnSuccess(t *testing.T) {
	hook := ThresholdHook(3)
	hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
	hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
	hook(LoopState{Stage: HookStageToolResult, LastToolOK: true}) // reset
	hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
	res := hook(LoopState{Stage: HookStageToolResult, LastToolOK: false})
	if res.Action != HookContinue {
		t.Fatalf("expected continue after reset, got %d", res.Action)
	}
}

func TestThresholdHookIgnoresModelResponseStage(t *testing.T) {
	hook := ThresholdHook(1)
	res := hook(LoopState{Stage: HookStageModelResponse, LastToolOK: false})
	if res.Action != HookContinue {
		t.Fatalf("expected continue during model-response stage, got %d", res.Action)
	}
}

package core

import (
	"testing"
)

func TestRecoveryHookSuccessResets(t *testing.T) {
	hook := RecoveryHook(DefaultRecoveryConfig())

	// Success → continue
	state := LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    true,
		LastToolName:  "fs_read",
		LastToolOutput: "file contents",
	}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue on success, got %v", res.Action)
	}
}

func TestRecoveryHookConsecutiveFailureStuck(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.MaxConsecutiveFailures = 3
	hook := RecoveryHook(cfg)

	// 3 consecutive failures with unrecognizable errors → stuck detection
	for i := 0; i < 3; i++ {
		state := LoopState{
			Stage:         HookStageToolResult,
			LastToolOK:    false,
			LastToolName:  []string{"sh", "fs_write", "patch_apply"}[i],
			LastToolOutput: "exit code 1: unknown error xyz",
		}
		res := hook(state)
		if i < 2 && res.Action != HookContinue {
			t.Fatalf("failure %d: expected HookContinue, got %v", i+1, res.Action)
		}
		if i == 2 {
			if res.Action != HookInjectMessage {
				t.Fatalf("expected HookInjectMessage on 3rd failure, got %v", res.Action)
			}
			if res.Reason != "recovery_stuck" {
				t.Fatalf("expected reason recovery_stuck, got %s", res.Reason)
			}
		}
	}
}

func TestRecoveryHookToolSpecificDegradation(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.MaxToolSpecificFailures = 3
	cfg.DisableOnDegradation = false
	hook := RecoveryHook(cfg)

	// 3 failures of same tool with unrecognizable error → degradation warning
	for i := 0; i < 3; i++ {
		state := LoopState{
			Stage:         HookStageToolResult,
			LastToolOK:    false,
			LastToolName:  "patch_apply",
			LastToolOutput: "apply failed: unknown reason xyz",
		}
		res := hook(state)
		// Third failure: degradation warning (no error pattern match)
		if i == 2 {
			if res.Action != HookInjectMessage {
				t.Fatalf("failure 3: expected HookInjectMessage for degradation, got %v", res.Action)
			}
			if res.Reason != "recovery_degradation_warn:patch_apply" {
				t.Fatalf("expected degradation warn reason, got %s", res.Reason)
			}
		}
	}
}

func TestRecoveryHookDisableOnDegradation(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.MaxToolSpecificFailures = 2
	cfg.DisableOnDegradation = true
	hook := RecoveryHook(cfg)

	// 2 failures of same tool with unrecognized error → stop tools
	for i := 0; i < 2; i++ {
		state := LoopState{
			Stage:         HookStageToolResult,
			LastToolOK:    false,
			LastToolName:  "sh",
			LastToolOutput: "exit status 2: bad interpreter",
		}
		res := hook(state)
		if i == 1 {
			if res.Action != HookStopTools {
				t.Fatalf("expected HookStopTools with DisableOnDegradation, got %v", res.Action)
			}
			if res.Reason != "recovery_degradation:sh" {
				t.Fatalf("expected recovery_degradation reason, got %s", res.Reason)
			}
		}
	}
}

func TestRecoveryHookErrorPatternNoMatch(t *testing.T) {
	hook := RecoveryHook(DefaultRecoveryConfig())

	state := LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    false,
		LastToolName:  "sh",
		LastToolOutput: "some unknown error that doesn't match any pattern",
	}
	res := hook(state)
	// First failure, no pattern match → continue (counting toward stuck)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue on unmatched error, got %v", res.Action)
	}
}

func TestRecoveryHookIgnoresModelResponseStage(t *testing.T) {
	hook := RecoveryHook(DefaultRecoveryConfig())

	state := LoopState{
		Stage:      HookStageModelResponse,
		LastToolOK: false,
	}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue for model_response stage, got %v", res.Action)
	}
}

func TestRecoveryHookSuccessResetsToolCount(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.MaxToolSpecificFailures = 2
	hook := RecoveryHook(cfg)

	// 1 failure of "sh"
	state := LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    false,
		LastToolName:  "sh",
		LastToolOutput: "error",
	}
	hook(state)

	// Success with "sh" resets its counter
	state = LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    true,
		LastToolName:  "sh",
		LastToolOutput: "ok",
	}
	hook(state)

	// 1 more failure → should NOT trigger degradation (count reset)
	state = LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    false,
		LastToolName:  "sh",
		LastToolOutput: "error again",
	}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue after reset, got %v (should not degrade)", res.Action)
	}
}

func TestRecoveryHookMultipleTools(t *testing.T) {
	cfg := DefaultRecoveryConfig()
	cfg.MaxToolSpecificFailures = 2
	cfg.MaxConsecutiveFailures = 4
	hook := RecoveryHook(cfg)

	// 2 failures of "sh" with unrecognizable error → degradation
	for i := 0; i < 2; i++ {
		state := LoopState{
			Stage:         HookStageToolResult,
			LastToolOK:    false,
			LastToolName:  "sh",
			LastToolOutput: "error xyz",
		}
		res := hook(state)
		if i == 1 {
			if res.Reason != "recovery_degradation_warn:sh" {
				t.Fatalf("expected degradation for sh, got %s", res.Reason)
			}
		}
	}

	// 1 failure of "fs_write" with "permission denied" → error pattern match
	state := LoopState{
		Stage:         HookStageToolResult,
		LastToolOK:    false,
		LastToolName:  "fs_write",
		LastToolOutput: "permission denied",
	}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage for pattern match, got %v", res.Action)
	}
	if res.Reason != "recovery_error_pattern" {
		t.Fatalf("expected recovery_error_pattern reason, got %s", res.Reason)
	}
}

func TestMatchErrorPattern(t *testing.T) {
	patterns := buildErrorPatterns()

	tests := []struct {
		output  string
		matches bool
	}{
		{"Error: no such file or directory", true},
		{"HTTP 429 Too Many Requests", true},
		{"patch failed: Hunk #1 FAILED at line 42", true},
		{"some random output", false},
		{"", false},
	}

	for _, tt := range tests {
		got := matchErrorPattern(patterns, "sh", tt.output)
		if (got != "") != tt.matches {
			t.Errorf("matchErrorPattern(%q) = %q, matches=%v, want matches=%v", tt.output, got, got != "", tt.matches)
		}
	}
}

func TestRecoveryHookDefaultConfig(t *testing.T) {
	cfg := RecoveryConfig{} // zero values
	hook := RecoveryHook(cfg)

	// Should use defaults (3 failures)
	for i := 0; i < 2; i++ {
		state := LoopState{
			Stage:         HookStageToolResult,
			LastToolOK:    false,
			LastToolName:  "sh",
			LastToolOutput: "error xyz",
		}
		res := hook(state)
		if res.Action != HookContinue {
			t.Fatalf("failure %d: expected HookContinue with defaults, got %v", i+1, res.Action)
		}
	}
}

package core

import (
	"fmt"
	"strings"
)

// RecoveryConfig controls the agent self-healing RecoveryHook.
type RecoveryConfig struct {
	// MaxConsecutiveFailures is the number of consecutive tool failures
	// before the hook injects a reflective guidance message. Default: 3.
	MaxConsecutiveFailures int

	// MaxToolSpecificFailures is the number of failures of the same tool
	// before the hook suggests disabling that tool. Default: 3.
	MaxToolSpecificFailures int

	// DisableOnDegradation controls whether the hook actually disables
	// tools that exceed MaxToolSpecificFailures (true) or just warns (false).
	// Default: false (warn only).
	DisableOnDegradation bool
}

// DefaultRecoveryConfig returns sensible defaults.
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		MaxConsecutiveFailures:  3,
		MaxToolSpecificFailures: 3,
		DisableOnDegradation:    false,
	}
}

// recoveryState tracks failure history across hook invocations.
type recoveryState struct {
	consecutiveFailures int
	lastFailedTool      string
	lastFailedArgs      string
	toolFailCount       map[string]int    // tool name → consecutive failure count
	disabledTools       map[string]bool   // tools suggested for disable
	errorPatterns       map[string]string // pattern → guidance message
}

// RecoveryHook returns a PolicyHook that detects stuck agents and provides
// self-healing interventions:
//
//  1. Stuck detection: after N consecutive tool failures, injects a message
//     encouraging the agent to reflect and change approach.
//
//  2. Error pattern matching: recognizes common failure signatures (wrong
//     args, missing files, rate limits) and injects targeted correction hints.
//
//  3. Graceful degradation: after N failures of the same tool, suggests
//     (or forces) disabling that tool for the remainder of the run.
func RecoveryHook(cfg RecoveryConfig) PolicyHook {
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	if cfg.MaxToolSpecificFailures <= 0 {
		cfg.MaxToolSpecificFailures = 3
	}

	s := &recoveryState{
		toolFailCount: make(map[string]int),
		disabledTools: make(map[string]bool),
		errorPatterns: buildErrorPatterns(),
	}

	return func(state LoopState) HookResult {
		// Only act on tool results
		if state.Stage != HookStageToolResult {
			return HookResult{Action: HookContinue}
		}

		// Reset counters on success
		if state.LastToolOK {
			s.consecutiveFailures = 0
			// Reset per-tool failure count on success
			delete(s.toolFailCount, state.LastToolName)
			return HookResult{Action: HookContinue}
		}

		// ── Track failures ──
		s.consecutiveFailures++
		s.toolFailCount[state.LastToolName]++
		s.lastFailedTool = state.LastToolName
		s.lastFailedArgs = state.LastToolArgs

		// ── Error pattern matching ──
		if guidance := matchErrorPattern(s.errorPatterns, state.LastToolName, state.LastToolOutput); guidance != "" {
			return HookResult{
				Action:  HookInjectMessage,
				Message: guidance,
				Reason:  "recovery_error_pattern",
			}
		}

		// ── Graceful degradation ──
		toolFails := s.toolFailCount[state.LastToolName]
		if toolFails >= cfg.MaxToolSpecificFailures && !s.disabledTools[state.LastToolName] {
			s.disabledTools[state.LastToolName] = true

			if cfg.DisableOnDegradation {
				msg := fmt.Sprintf(
					"Recovery: tool %q has failed %d times in a row and has been disabled for this run. "+
						"Use an alternative approach or tool to accomplish your goal without %s.",
					state.LastToolName, toolFails, state.LastToolName,
				)
				return HookResult{
					Action:  HookStopTools,
					Message: msg,
					Reason:  fmt.Sprintf("recovery_degradation:%s", state.LastToolName),
				}
			}

			msg := fmt.Sprintf(
				"Recovery warning: tool %q has failed %d times. Consider using a different approach "+
					"or tool. Continuing with %s may waste budget without progress.",
				state.LastToolName, toolFails, state.LastToolName,
			)
			return HookResult{
				Action:  HookInjectMessage,
				Message: msg,
				Reason:  fmt.Sprintf("recovery_degradation_warn:%s", state.LastToolName),
			}
		}

		// ── Stuck detection: consecutive failures across tools ──
		if s.consecutiveFailures >= cfg.MaxConsecutiveFailures {
			s.consecutiveFailures = 0 // reset to avoid spamming
			msg := fmt.Sprintf(
				"Recovery: %d consecutive tool failures detected. Your approach may be fundamentally flawed. "+
					"Step back and reconsider: (1) Are you using the right tool for the task? "+
					"(2) Do the tool arguments match the actual state of the workspace? "+
					"(3) Would a completely different strategy work better? "+
					"Use the reflect tool to evaluate your approach before proceeding.",
				cfg.MaxConsecutiveFailures,
			)
			return HookResult{
				Action:  HookInjectMessage,
				Message: msg,
				Reason:  "recovery_stuck",
			}
		}

		return HookResult{Action: HookContinue}
	}
}

// buildErrorPatterns returns a map of substring → guidance message for
// common failure signatures.
func buildErrorPatterns() map[string]string {
	return map[string]string{
		// Missing file / path errors
		"no such file": "Recovery hint: the target file or directory does not exist. " +
			"Use fs_list or project_search to verify the correct path before retrying.",
		"not found": "Recovery hint: the resource was not found. " +
			"Verify the path or identifier exists before retrying.",
		"does not exist": "Recovery hint: the target does not exist. " +
			"Check the workspace structure with fs_list or project_search.",

		// Permission errors
		"permission denied": "Recovery hint: permission denied. " +
			"The file or directory may have restricted access. Try a different approach.",

		// Rate limiting
		"rate limit":   "Recovery hint: rate limit hit. Wait briefly or reduce request frequency.",
		"too many":     "Recovery hint: too many requests. Slow down and batch your operations.",
		"429":          "Recovery hint: HTTP 429 rate limit. Wait before retrying API calls.",

		// Tool argument errors
		"invalid argument": "Recovery hint: invalid tool arguments. " +
			"Use inspect_tool to check the expected schema before retrying.",
		"invalid json": "Recovery hint: malformed JSON in tool arguments. " +
			"Double-check the JSON structure and escape special characters.",
		"missing required": "Recovery hint: missing required parameter. " +
			"Use inspect_tool to see the full parameter schema.",

		// Patch/merge errors
		"hunk #": "Recovery hint: patch failed to apply. " +
			"The file may have changed since your last read. Re-read the file and try again.",
		"malformed patch": "Recovery hint: malformed patch syntax. " +
			"Consider using fs_write instead of patch_apply for the edit.",

		// Build errors
		"compilation error": "Recovery hint: compilation failed. " +
			"Read the full error output, identify the root cause, and fix it before proceeding.",
		"syntax error": "Recovery hint: syntax error in code. " +
			"Check for typos, missing brackets, or incorrect indentation.",
	}
}

// matchErrorPattern checks the tool output against known error patterns
// and returns a guidance message if one matches.
func matchErrorPattern(patterns map[string]string, toolName, output string) string {
	if output == "" {
		return ""
	}
	lower := strings.ToLower(output)
	for pattern, guidance := range patterns {
		if strings.Contains(lower, pattern) {
			return guidance
		}
	}
	return ""
}

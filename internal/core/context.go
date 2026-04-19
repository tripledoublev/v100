package core

import (
	"fmt"
)

// PressureMonitor returns a PolicyHook that tracks context window saturation
// and emits warnings or triggers compression when pressure exceeds the
// configured threshold. This enables proactive context management before
// hitting hard limits.
//
// The hook reads ContextPressure and ContextWindowSize from LoopState
// (populated by runHooks). When pressure exceeds the threshold it returns
// HookContinue with a logged warning at first breach; if pressure continues
// to rise above threshold * 1.15 it forces compression.
func PressureMonitor(threshold float64) PolicyHook {
	if threshold <= 0 {
		threshold = 0.70
	}
	var warned bool

	return func(state LoopState) HookResult {
		if state.ContextWindowSize <= 0 || state.ContextPressure <= 0 {
			return HookResult{Action: HookContinue}
		}

		p := state.ContextPressure

		// Reset warned flag if pressure drops below threshold
		if p < threshold {
			warned = false
			return HookResult{Action: HookContinue}
		}

		// First breach: emit a warning message to guide the agent
		if !warned {
			warned = true
			msg := fmt.Sprintf(
				"Context pressure is %.0f%% (estimated %d / %d tokens). "+
					"Consider being more concise or summarizing earlier results.",
				p*100, estimateTokensFromState(state), state.ContextWindowSize,
			)
			return HookResult{
				Action:  HookInjectMessage,
				Message: msg,
				Reason:  "context_pressure_warn",
			}
		}

		// Sustained high pressure: force compression
		if p > threshold*1.15 {
			warned = false // reset so we can warn again after compression
			return HookResult{
				Action:  HookForceReplan,
				Message: "context_pressure_compress",
				Reason:  fmt.Sprintf("context pressure %.0f%% exceeds %.0f%% — forcing compression", p*100, threshold*1.15*100),
			}
		}

		return HookResult{Action: HookContinue}
	}
}

// estimateTokensFromState approximates token usage from LoopState.
// Uses the message count as a rough proxy when exact data is unavailable.
func estimateTokensFromState(state LoopState) int {
	// The LoopState doesn't carry the actual message slice, but
	// the pressure ratio is computed from actual estimates in runHooks.
	// We back-compute: tokens = pressure * contextSize
	if state.ContextPressure > 0 && state.ContextWindowSize > 0 {
		return int(state.ContextPressure * float64(state.ContextWindowSize))
	}
	return 0
}

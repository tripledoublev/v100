package core

import (
	"testing"
)

func TestPressureMonitorNoPressure(t *testing.T) {
	hook := PressureMonitor(0.70)

	// No context size info — should continue
	state := LoopState{ContextPressure: 0, ContextWindowSize: 0}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue with no context info, got %v", res.Action)
	}
}

func TestPressureMonitorBelowThreshold(t *testing.T) {
	hook := PressureMonitor(0.70)

	state := LoopState{ContextPressure: 0.50, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue below threshold, got %v", res.Action)
	}
}

func TestPressureMonitorWarnOnFirstBreach(t *testing.T) {
	hook := PressureMonitor(0.70)

	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}
	if res.Reason != "context_pressure_warn" {
		t.Fatalf("expected reason context_pressure_warn, got %s", res.Reason)
	}
}

func TestPressureMonitorSustainedHighForcesReplan(t *testing.T) {
	hook := PressureMonitor(0.70)

	// First breach: warn
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}

	// Sustained high pressure (above threshold * 1.15 = 0.805): force replan
	state = LoopState{ContextPressure: 0.85, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookForceReplan {
		t.Fatalf("expected HookForceReplan at sustained high pressure, got %v", res.Action)
	}
}

func TestPressureMonitorResetsAfterDrop(t *testing.T) {
	hook := PressureMonitor(0.70)

	// First breach
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on first breach, got %v", res.Action)
	}

	// Drop below threshold — should reset
	state = LoopState{ContextPressure: 0.50, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue after pressure drops, got %v", res.Action)
	}

	// Breach again — should warn again (not replan)
	state = LoopState{ContextPressure: 0.72, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage on re-breach after reset, got %v", res.Action)
	}
}

func TestPressureMonitorDefaultThreshold(t *testing.T) {
	// threshold=0 should default to 0.70
	hook := PressureMonitor(0)

	// Below 0.70: continue
	state := LoopState{ContextPressure: 0.60, ContextWindowSize: 128000}
	res := hook(state)
	if res.Action != HookContinue {
		t.Fatalf("expected HookContinue below default threshold, got %v", res.Action)
	}

	// At 0.75: warn
	state = LoopState{ContextPressure: 0.75, ContextWindowSize: 128000}
	res = hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage above default threshold, got %v", res.Action)
	}
}

func TestPressureMonitorCustomThreshold(t *testing.T) {
	hook := PressureMonitor(0.50)

	// At 0.55: warn (above 0.50 threshold)
	state := LoopState{ContextPressure: 0.55, ContextWindowSize: 100000}
	res := hook(state)
	if res.Action != HookInjectMessage {
		t.Fatalf("expected HookInjectMessage with custom threshold, got %v", res.Action)
	}
}

func TestEstimateTokensFromState(t *testing.T) {
	state := LoopState{ContextPressure: 0.75, ContextWindowSize: 100000}
	est := estimateTokensFromState(state)
	if est != 75000 {
		t.Fatalf("expected 75000, got %d", est)
	}

	// Zero values
	state = LoopState{}
	est = estimateTokensFromState(state)
	if est != 0 {
		t.Fatalf("expected 0 for empty state, got %d", est)
	}
}

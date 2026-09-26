package core

import (
	"testing"

	"github.com/tripledoublev/v100/internal/policy"
)

func TestSynthesisWatchdogMessageReadHeavy(t *testing.T) {
	msg, reason, action, ok := synthesisWatchdogMessage(
		6,
		6,
		2,
		readHeavyWatchdogTokenThreshold,
		false,
	)
	if !ok {
		t.Fatal("expected watchdog trigger")
	}
	if reason != "read_heavy_watchdog" {
		t.Fatalf("reason = %q, want read_heavy_watchdog", reason)
	}
	if msg == "" {
		t.Fatal("expected non-empty watchdog message")
	}
	if action != HookStopTools {
		t.Fatalf("action = %v, want HookStopTools", action)
	}
}

func TestSynthesisWatchdogMessageDoesNotTriggerOnLowInspectionShare(t *testing.T) {
	if _, _, _, ok := synthesisWatchdogMessage(
		10,
		5,
		3,
		readHeavyWatchdogTokenThreshold+1,
		false,
	); ok {
		t.Fatal("did not expect watchdog trigger")
	}
}

func TestSynthesisWatchdogMessageInspectionOnly(t *testing.T) {
	msg, reason, action, ok := synthesisWatchdogMessage(
		inspectionWatchdogToolThreshold, // 8, meets the tool threshold
		8,                               // inspectionToolCalls — all inspection tools
		inspectionWatchdogModelThreshold, // 3, meets the model-call threshold
		1,                               // stepTokensUsed — irrelevant for this branch
		true,                            // inspectionOnly = all tools are inspection tools
	)
	if !ok {
		t.Fatal("expected watchdog trigger for inspection-only branch")
	}
	if reason != "inspection_watchdog" {
		t.Fatalf("reason = %q, want inspection_watchdog", reason)
	}
	if msg == "" {
		t.Fatal("expected non-empty watchdog message")
	}
	if action != HookStopTools {
		t.Fatalf("action = %v, want HookStopTools", action)
	}
}

func TestSynthesisWatchdogMessageNoTrigger(t *testing.T) {
	cases := []struct {
		name                string
		toolCallsUsed       int
		inspectionToolCalls int
		modelCalls          int
		stepTokensUsed      int
		inspectionOnly       bool
	}{
		{
			name:          "inspectionOnly_below_tool_threshold",
			toolCallsUsed: inspectionWatchdogToolThreshold - 1, // 7 < 8
			modelCalls:    inspectionWatchdogModelThreshold,
			inspectionOnly: true,
		},
		{
			name:          "inspectionOnly_below_model_threshold",
			toolCallsUsed: inspectionWatchdogToolThreshold,
			modelCalls:    inspectionWatchdogModelThreshold - 1, // 2 < 3
			inspectionOnly: true,
		},
		{
			name:          "readHeavy_below_token_threshold",
			toolCallsUsed: readHeavyWatchdogToolThreshold,
			modelCalls:    readHeavyWatchdogModelThreshold,
			stepTokensUsed: readHeavyWatchdogTokenThreshold - 1, // below threshold
			inspectionToolCalls: readHeavyWatchdogToolThreshold,
			inspectionOnly: false,
		},
		{
			name:          "readHeavy_below_model_call_threshold",
			toolCallsUsed: readHeavyWatchdogToolThreshold,
			modelCalls:    readHeavyWatchdogModelThreshold - 1, // 1 < 2
			stepTokensUsed: readHeavyWatchdogTokenThreshold,
			inspectionToolCalls: readHeavyWatchdogToolThreshold,
			inspectionOnly: false,
		},
		{
			name:          "readHeavy_inspection_share_too_low",
			toolCallsUsed: 10,
			modelCalls:    readHeavyWatchdogModelThreshold,
			stepTokensUsed: readHeavyWatchdogTokenThreshold,
			inspectionToolCalls: 4, // 4*5 < 10*4 → 20 < 40, ratio check fails
			inspectionOnly: false,
		},
		{
			name:          "readHeavy_below_inspection_tool_threshold",
			toolCallsUsed: 6,
			modelCalls:    readHeavyWatchdogModelThreshold,
			stepTokensUsed: readHeavyWatchdogTokenThreshold,
			inspectionToolCalls: readHeavyWatchdogToolThreshold - 1, // 5 < 6
			inspectionOnly: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := synthesisWatchdogMessage(
				tc.toolCallsUsed,
				tc.inspectionToolCalls,
				tc.modelCalls,
				tc.stepTokensUsed,
				tc.inspectionOnly,
			); ok {
				t.Errorf("case %q: expected no trigger, got ok=true", tc.name)
			}
		})
	}
}

// TestSynthesisWatchdogMessageReadHeavyWithMixedTools verifies the read-heavy
// watchdog fires even when not all tools are inspection tools, as long as the
// inspection-tool share exceeds 80% (4/5 ratio check).
func TestSynthesisWatchdogMessageReadHeavyWithMixedTools(t *testing.T) {
	msg, reason, action, ok := synthesisWatchdogMessage(
		10,    // total tool calls
		9,     // 9 inspection tools, 1 non-inspection → 90% ratio (≥80% → fires)
		3,     // modelCalls >= readHeavyWatchdogModelThreshold (2)
		readHeavyWatchdogTokenThreshold,
		false, // not inspectionOnly — should still fire via read-heavy branch
	)
	if !ok {
		t.Fatal("expected watchdog trigger for read-heavy mixed tools")
	}
	if reason != "read_heavy_watchdog" {
		t.Fatalf("reason = %q, want read_heavy_watchdog", reason)
	}
	if msg == "" {
		t.Fatal("expected non-empty watchdog message")
	}
	if action != HookStopTools {
		t.Fatalf("action = %v, want HookStopTools", action)
	}
}

func TestSynthesisWatchdogRespectsInspectionLimit(t *testing.T) {
	// 8 inspection-only calls trip the default watchdog.
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(0, 8, 8, 9, 1000, true, false); !ok || reason != "inspection_watchdog" {
		t.Fatalf("default limit: ok=%v reason=%q", ok, reason)
	}
	// A raised limit lets the same step continue, including past the
	// read-heavy tool threshold when step tokens are high.
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(24, 8, 8, 9, 50000, true, false); ok {
		t.Fatalf("raised limit fired early: %q", reason)
	}
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(24, 24, 24, 25, 1000, true, false); !ok || reason != "inspection_watchdog" {
		t.Fatalf("raised limit at threshold: ok=%v reason=%q", ok, reason)
	}
}

func TestInspectionToolLimitFromPolicy(t *testing.T) {
	if got := inspectionToolLimit(nil); got != inspectionWatchdogToolThreshold {
		t.Fatalf("nil loop = %d", got)
	}
	l := &Loop{Policy: &policy.Policy{InspectionToolLimit: 30}}
	if got := inspectionToolLimit(l); got != 30 {
		t.Fatalf("policy limit = %d", got)
	}
}

func TestReadHeavyWatchdogSkipsAfterEdit(t *testing.T) {
	// The 2026-09-25 jsroy run: 18 of 19 calls were reads, ~71k step tokens,
	// with one patch_apply already applied. Before the edit this trips the
	// read-heavy watchdog; after it, reads are verification.
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(24, 19, 18, 12, 71600, false, false); !ok || reason != "read_heavy_watchdog" {
		t.Fatalf("without edit: ok=%v reason=%q", ok, reason)
	}
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(24, 19, 18, 12, 71600, false, true); ok {
		t.Fatalf("after edit fired: %q", reason)
	}
	for _, name := range []string{"patch_apply", "fs_write", "fs_mkdir"} {
		if !isEditTool(name) {
			t.Fatalf("%s should be an edit tool", name)
		}
	}
	if isEditTool("fs_read") {
		t.Fatal("fs_read is not an edit tool")
	}
}

func TestReadHeavyThresholdDoesNotOverflow(t *testing.T) {
	huge := int(^uint(0) >> 1)
	// With an enormous limit the read-heavy threshold must stay enormous,
	// not wrap around to a tiny value that fires immediately.
	if _, reason, _, ok := synthesisWatchdogMessageWithLimit(huge, 50, 50, 20, 500000, false, false); ok {
		t.Fatalf("fired with huge limit: %q", reason)
	}
}

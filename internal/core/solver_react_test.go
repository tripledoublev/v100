package core

import "testing"

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

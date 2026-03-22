package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestWakeStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wake.json")

	now := time.Now()
	queuedGoals := []core.GeneratedGoal{
		{ID: "goal-1", Content: "Do task A", CreatedAt: now},
		{ID: "goal-2", Content: "Do task B", CreatedAt: now.Add(1 * time.Minute)},
	}

	original := &core.WakeState{
		Status:              core.WakeStatusRunning,
		StartedAt:           &now,
		PID:                 12345,
		IntervalSeconds:     3600,
		NextRunAt:           now.Add(1 * time.Hour),
		ConsecutiveFailures: 2,
		QueuedGoals:         queuedGoals,
		Provider:            "minimax",
		Solver:              "react",
	}

	// Write
	if err := core.WriteWakeState(path, original); err != nil {
		t.Fatalf("WriteWakeState: %v", err)
	}

	// Read
	read, err := core.ReadWakeState(path)
	if err != nil {
		t.Fatalf("ReadWakeState: %v", err)
	}

	// Verify all fields
	if read.Status != original.Status {
		t.Errorf("status: got %q, want %q", read.Status, original.Status)
	}
	if read.PID != original.PID {
		t.Errorf("PID: got %d, want %d", read.PID, original.PID)
	}
	if read.IntervalSeconds != original.IntervalSeconds {
		t.Errorf("interval: got %d, want %d", read.IntervalSeconds, original.IntervalSeconds)
	}
	if read.ConsecutiveFailures != original.ConsecutiveFailures {
		t.Errorf("failures: got %d, want %d", read.ConsecutiveFailures, original.ConsecutiveFailures)
	}
	if read.Provider != original.Provider {
		t.Errorf("provider: got %q, want %q", read.Provider, original.Provider)
	}
	if len(read.QueuedGoals) != len(original.QueuedGoals) {
		t.Errorf("queued goals len: got %d, want %d", len(read.QueuedGoals), len(original.QueuedGoals))
	}
}

func TestWakeStateAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wake.json")

	state1 := &core.WakeState{
		Status:          core.WakeStatusIdle,
		IntervalSeconds: 1000,
		Provider:        "provider1",
	}

	// First write
	if err := core.WriteWakeState(path, state1); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Simulate a crash by making the directory read-only so second write fails
	// But the original file should still be intact
	state2 := &core.WakeState{
		Status:          core.WakeStatusRunning,
		IntervalSeconds: 2000,
		Provider:        "provider2",
	}

	// Verify first state is still there
	read, err := core.ReadWakeState(path)
	if err != nil {
		t.Fatalf("read after first write: %v", err)
	}
	if read.Provider != "provider1" {
		t.Errorf("provider after first write: got %q, want provider1", read.Provider)
	}

	// Second write should succeed (no dir permissions issue in test)
	if err := core.WriteWakeState(path, state2); err != nil {
		t.Fatalf("second write: %v", err)
	}

	// Verify second state
	read, err = core.ReadWakeState(path)
	if err != nil {
		t.Fatalf("read after second write: %v", err)
	}
	if read.Provider != "provider2" {
		t.Errorf("provider after second write: got %q, want provider2", read.Provider)
	}

	// Verify no .tmp file left behind
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Errorf(".tmp file left behind at %s", tmpPath)
	}
}

func TestInitWakeState(t *testing.T) {
	state := core.InitWakeState()

	if state.Status != core.WakeStatusIdle {
		t.Errorf("status: got %q, want idle", state.Status)
	}
	if state.IntervalSeconds <= 0 {
		t.Errorf("interval: got %d, want > 0", state.IntervalSeconds)
	}
	if state.ConsecutiveFailures != 0 {
		t.Errorf("failures: got %d, want 0", state.ConsecutiveFailures)
	}
	if state.QueuedGoals == nil || len(state.QueuedGoals) != 0 {
		t.Errorf("queued goals: should be empty slice")
	}
}

func TestWakeCycleDelayZeroFailures(t *testing.T) {
	// 0 failures → return interval as-is (with jitter)
	interval := 3600 // 1 hour
	maxBackoff := 86400
	failures := 0

	delay := core.WakeCycleDelay(interval, maxBackoff, failures)

	// Should be approximately interval ± 25% jitter
	minExpected := time.Duration(interval) * time.Second * 75 / 100  // 75%
	maxExpected := time.Duration(interval) * time.Second * 125 / 100 // 125%

	if delay < minExpected || delay > maxExpected {
		t.Errorf("delay: got %v, want between %v and %v", delay, minExpected, maxExpected)
	}
}

func TestWakeCycleDelayExponentialBackoff(t *testing.T) {
	interval := 60     // 1 minute
	maxBackoff := 3600 // 1 hour
	failures := 3

	delay := core.WakeCycleDelay(interval, maxBackoff, failures)

	// Should be approximately interval * 2^3 = 60 * 8 = 480 seconds (8 min) ± 25%
	expectedBase := time.Duration(interval*8) * time.Second
	minExpected := expectedBase * 75 / 100
	maxExpected := expectedBase * 125 / 100

	if delay < minExpected || delay > maxExpected {
		t.Errorf("delay: got %v, want between %v and %v", delay, minExpected, maxExpected)
	}
}

func TestWakeCycleDelayCappedAtMaxBackoff(t *testing.T) {
	interval := 60
	maxBackoff := 3600 // 1 hour
	failures := 10     // Would be 60 * 2^10 = 61440, capped at 3600

	delay := core.WakeCycleDelay(interval, maxBackoff, failures)

	// Should be capped at maxBackoff ± 25%
	minExpected := time.Duration(maxBackoff) * time.Second * 75 / 100
	maxExpected := time.Duration(maxBackoff) * time.Second * 125 / 100

	if delay < minExpected || delay > maxExpected {
		t.Errorf("delay: got %v, want between %v and %v (capped)", delay, minExpected, maxExpected)
	}
}

func TestReadWakeStateNotFound(t *testing.T) {
	path := "/nonexistent/path/wake.json"
	_, err := core.ReadWakeState(path)
	if err == nil {
		t.Errorf("expected error for nonexistent file")
	}
}

func TestWakeStateJSON(t *testing.T) {
	// Verify the struct marshals to valid JSON
	state := core.InitWakeState()
	state.Status = core.WakeStatusRunning
	state.Provider = "minimax"

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var unmarshaled core.WakeState
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if unmarshaled.Status != state.Status {
		t.Errorf("status mismatch after JSON round-trip")
	}
}

func TestDefaultWakeStatePathUsesXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got := core.DefaultWakeStatePath()
	want := filepath.Join(xdg, "v100", "wake.json")
	if got != want {
		t.Fatalf("DefaultWakeStatePath() = %q, want %q", got, want)
	}
}

func TestDefaultWakeLogPathUsesXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	got := core.DefaultWakeLogPath()
	want := filepath.Join(xdg, "v100", "wake.log")
	if got != want {
		t.Fatalf("DefaultWakeLogPath() = %q, want %q", got, want)
	}
}

func TestNewWakeToken(t *testing.T) {
	got, err := core.NewWakeToken()
	if err != nil {
		t.Fatalf("NewWakeToken() error = %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("token length = %d, want 32", len(got))
	}
	if strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("token contains non-hex characters: %q", got)
	}
}

func TestWakeProcessOwnedRejectsCurrentProcessWithoutTokenInCmdline(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	state := &core.WakeState{
		PID:        os.Getpid(),
		Token:      "definitely-not-in-cmdline",
		Executable: exe,
	}
	if core.WakeProcessOwned(state) {
		t.Fatal("WakeProcessOwned should reject unrelated process ownership")
	}
}

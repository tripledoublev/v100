package providers

import (
	"errors"
	"testing"
	"time"
)

func TestHealthTracker_FreshIsHealthy(t *testing.T) {
	h := NewHealthTracker("p")
	if !h.IsHealthy() {
		t.Fatal("fresh tracker should be healthy")
	}
}

func TestHealthTracker_GoesUnhealthyAtMaxErr(t *testing.T) {
	h := NewHealthTracker("p")
	for range h.maxErr {
		h.RecordError(errors.New("boom"), time.Millisecond)
	}
	if h.IsHealthy() {
		t.Fatal("tracker should be unhealthy after maxErr consecutive errors")
	}
}

// Regression: prior to the fix, once `time.Since(unhealthyAt) > cooldown`,
// IsHealthy() would return true on every subsequent call indefinitely, even
// though the provider remained unhealthy. The contract is: one probe per
// cooldown window.
func TestHealthTracker_ProbeGatedByCooldown(t *testing.T) {
	h := NewHealthTracker("p")
	h.cooldown = 20 * time.Millisecond
	for range h.maxErr {
		h.RecordError(errors.New("boom"), time.Millisecond)
	}
	if h.IsHealthy() {
		t.Fatal("expected unhealthy before cooldown")
	}

	time.Sleep(30 * time.Millisecond)
	if !h.IsHealthy() {
		t.Fatal("expected probe to be allowed after cooldown")
	}
	// The probe call above should have re-armed unhealthyAt. A second
	// immediate call must NOT return true — that was the bug.
	if h.IsHealthy() {
		t.Fatal("expected only one probe per cooldown window; second call should be false")
	}
}

func TestHealthTracker_RecordSuccessClearsUnhealthyAtWhenRecovered(t *testing.T) {
	h := NewHealthTracker("p")
	h.window = time.Hour // keep events in window
	h.minRate = 0.5
	// Record one error so unhealthyAt is potentially set if we trip maxErr.
	// Force status to unhealthy by saturating errors.
	for range h.maxErr {
		h.RecordError(errors.New("boom"), time.Millisecond)
	}
	if h.unhealthyAt.IsZero() {
		t.Fatal("expected unhealthyAt to be set after maxErr errors")
	}

	// Loosen thresholds so the next RecordSuccess flips status to healthy
	// without waiting for errors to age out of the window.
	h.mu.Lock()
	h.maxErr = 100
	h.minRate = 0
	h.mu.Unlock()

	h.RecordSuccess(time.Millisecond)
	if !h.unhealthyAt.IsZero() {
		t.Fatal("RecordSuccess should clear unhealthyAt when status returns to healthy")
	}
}

func TestHealthTracker_StatusFields(t *testing.T) {
	h := NewHealthTracker("p")
	h.RecordSuccess(10 * time.Millisecond)
	h.RecordSuccess(30 * time.Millisecond)
	h.RecordError(errors.New("oops"), 20*time.Millisecond)

	s := h.Status()
	if s.Provider != "p" {
		t.Errorf("provider=%q want p", s.Provider)
	}
	if s.TotalCalls != 3 {
		t.Errorf("TotalCalls=%d want 3", s.TotalCalls)
	}
	if s.TotalErrors != 1 {
		t.Errorf("TotalErrors=%d want 1", s.TotalErrors)
	}
	if s.LastError != "oops" {
		t.Errorf("LastError=%q want oops", s.LastError)
	}
	if s.AvgLatencyMs != 20 {
		t.Errorf("AvgLatencyMs=%d want 20", s.AvgLatencyMs)
	}
}

func TestFormatHealthStatus_Empty(t *testing.T) {
	got := FormatHealthStatus(nil)
	if got == "" {
		t.Fatal("expected non-empty message for empty input")
	}
}

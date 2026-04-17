package providers

import (
	"fmt"
	"sync"
	"time"
)

// HealthStatus summarizes a provider's recent health.
type HealthStatus struct {
	Provider      string  `json:"provider"`
	Healthy       bool    `json:"healthy"`
	SuccessRate   float64 `json:"success_rate"`
	AvgLatencyMs  int64   `json:"avg_latency_ms"`
	TotalCalls    int     `json:"total_calls"`
	TotalErrors   int     `json:"total_errors"`
	LastError     string  `json:"last_error,omitempty"`
	LastSuccessAt string  `json:"last_success_at,omitempty"`
	LastFailureAt string  `json:"last_failure_at,omitempty"`
}

// healthEvent is a single recorded outcome.
type healthEvent struct {
	ts      time.Time
	latency time.Duration
	err     error
}

// HealthTracker records provider call outcomes and computes rolling health.
type HealthTracker struct {
	mu      sync.Mutex
	name    string
	window  time.Duration
	events  []healthEvent
	maxErr  int           // max errors in window before marking unhealthy
	minRate float64       // minimum success rate (0-1) to be healthy
	cooldown time.Duration // minimum time after going unhealthy before retry
	unhealthyAt time.Time
}

// NewHealthTracker creates a tracker for a named provider.
func NewHealthTracker(name string) *HealthTracker {
	return &HealthTracker{
		name:    name,
		window:  5 * time.Minute,
		maxErr:  5,
		minRate: 0.5,
		cooldown: 30 * time.Second,
	}
}

// RecordSuccess records a successful provider call.
func (h *HealthTracker) RecordSuccess(latency time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, healthEvent{ts: time.Now(), latency: latency})
	h.trim()
	if h.statusLocked().Healthy {
		h.unhealthyAt = time.Time{}
	}
}

// RecordError records a failed provider call.
func (h *HealthTracker) RecordError(err error, latency time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, healthEvent{ts: time.Now(), latency: latency, err: err})
	h.trim()
	// Check if we just became unhealthy
	status := h.statusLocked()
	if !status.Healthy {
		h.unhealthyAt = time.Now()
	}
}

// IsHealthy returns whether the provider should be used for new requests.
// While unhealthy, callers get a single probe per cooldown window: returning
// true arms the next cooldown so concurrent callers don't all probe at once.
// RecordSuccess/RecordError update the underlying status from the probe result.
func (h *HealthTracker) IsHealthy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.statusLocked().Healthy {
		return true
	}
	if !h.unhealthyAt.IsZero() && time.Since(h.unhealthyAt) > h.cooldown {
		h.unhealthyAt = time.Now()
		return true
	}
	return false
}

// Status returns the current health summary.
func (h *HealthTracker) Status() HealthStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked()
}

func (h *HealthTracker) statusLocked() HealthStatus {
	now := time.Now()
	cutoff := now.Add(-h.window)
	var successes, errors int
	var totalLatency time.Duration
	var lastErr error
	var lastSuccess, lastFailure time.Time

	for _, ev := range h.events {
		if ev.ts.Before(cutoff) {
			continue
		}
		totalLatency += ev.latency
		if ev.err == nil {
			successes++
			if ev.ts.After(lastSuccess) {
				lastSuccess = ev.ts
			}
		} else {
			errors++
			lastErr = ev.err
			if ev.ts.After(lastFailure) {
				lastFailure = ev.ts
			}
		}
	}

	total := successes + errors
	rate := 0.0
	if total > 0 {
		rate = float64(successes) / float64(total)
	}

	var avgMs int64
	if total > 0 {
		avgMs = totalLatency.Milliseconds() / int64(total)
	}

	healthy := errors < h.maxErr
	if total >= 3 && rate < h.minRate {
		healthy = false
	}

	s := HealthStatus{
		Provider:     h.name,
		Healthy:      healthy,
		SuccessRate:  rate,
		AvgLatencyMs: avgMs,
		TotalCalls:   total,
		TotalErrors:  errors,
	}
	if lastErr != nil {
		s.LastError = lastErr.Error()
	}
	if !lastSuccess.IsZero() {
		s.LastSuccessAt = lastSuccess.Format(time.RFC3339)
	}
	if !lastFailure.IsZero() {
		s.LastFailureAt = lastFailure.Format(time.RFC3339)
	}
	return s
}

func (h *HealthTracker) trim() {
	cutoff := time.Now().Add(-h.window)
	i := 0
	for i < len(h.events) && h.events[i].ts.Before(cutoff) {
		i++
	}
	if i > 0 {
		h.events = h.events[i:]
	}
}

// FormatHealthStatus renders a health summary as a formatted string.
func FormatHealthStatus(statuses []HealthStatus) string {
	if len(statuses) == 0 {
		return "No provider health data available.\n"
	}
	var b string
	for _, s := range statuses {
		icon := "✓"
		if !s.Healthy {
			icon = "✗"
		}
		b += fmt.Sprintf("  %s %-20s  rate=%.0f%%  avg=%dms  calls=%d  errors=%d",
			icon, s.Provider, s.SuccessRate*100, s.AvgLatencyMs, s.TotalCalls, s.TotalErrors)
		if s.LastError != "" {
			b += fmt.Sprintf("  last_err=%q", s.LastError)
		}
		b += "\n"
	}
	return b
}

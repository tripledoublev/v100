package tools

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func replaceDefaultExternalAPISafetyForTest(t *testing.T, safety *externalAPISafety) {
	t.Helper()
	old := defaultExternalAPISafety
	defaultExternalAPISafety = safety
	t.Cleanup(func() {
		defaultExternalAPISafety = old
	})
}

func TestClampExternalPageLimit(t *testing.T) {
	tests := []struct {
		name         string
		requested    int
		defaultLimit int
		want         int
	}{
		{name: "default", requested: 0, defaultLimit: 20, want: 20},
		{name: "negative", requested: -5, defaultLimit: 8, want: 8},
		{name: "inside cap", requested: 42, defaultLimit: 8, want: 42},
		{name: "above cap", requested: 250, defaultLimit: 8, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampExternalPageLimit(tt.requested, tt.defaultLimit); got != tt.want {
				t.Fatalf("clampExternalPageLimit(%d, %d) = %d, want %d", tt.requested, tt.defaultLimit, got, tt.want)
			}
		})
	}
}

func TestExternalAPISafetyEnforcesRateLimitPerEndpoint(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	safety := newExternalAPISafety(func() time.Time { return now }, externalAPISafetyPolicy{
		RatePerSecond:      1,
		Burst:              2,
		BreakerThreshold:   3,
		BreakerBaseBackoff: time.Second,
		BreakerMaxBackoff:  time.Minute,
	})

	if err := safety.before("news_fetch:https://example.com/rss"); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}
	if err := safety.before("news_fetch:https://example.com/rss"); err != nil {
		t.Fatalf("second burst request should pass: %v", err)
	}
	if err := safety.before("news_fetch:https://example.com/rss"); err == nil {
		t.Fatal("third request should be rate-limited")
	} else if !strings.Contains(err.Error(), `"kind":"rate_limit_exceeded"`) {
		t.Fatalf("rate-limit error should be structured, got: %s", err.Error())
	}

	if err := safety.before("news_fetch:https://other.example/rss"); err != nil {
		t.Fatalf("different endpoint should have its own bucket: %v", err)
	}

	now = now.Add(time.Second)
	if err := safety.before("news_fetch:https://example.com/rss"); err != nil {
		t.Fatalf("token should refill after one second: %v", err)
	}
}

func TestExternalAPISafetyCircuitBreakerBackoff(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	safety := newExternalAPISafety(func() time.Time { return now }, externalAPISafetyPolicy{
		RatePerSecond:      100,
		Burst:              100,
		BreakerThreshold:   3,
		BreakerBaseBackoff: 2 * time.Second,
		BreakerMaxBackoff:  time.Minute,
	})
	endpoint := "atproto:app.bsky.feed.getTimeline"

	for i := 0; i < 2; i++ {
		if err := safety.before(endpoint); err != nil {
			t.Fatalf("request %d should pass before breaker opens: %v", i+1, err)
		}
		err := safety.after(endpoint, true, errors.New("HTTP 429"))
		if err == nil {
			t.Fatalf("remote rate-limit response %d should produce a structured alert", i+1)
		}
		if !strings.Contains(err.Error(), `"kind":"remote_rate_limit"`) {
			t.Fatalf("expected remote_rate_limit alert, got: %s", err.Error())
		}
	}

	if err := safety.before(endpoint); err != nil {
		t.Fatalf("third request should pass before recording the third remote 429: %v", err)
	}
	err := safety.after(endpoint, true, errors.New("HTTP 429"))
	if err == nil {
		t.Fatal("third remote 429 should open the breaker")
	}
	if got := err.Error(); !strings.Contains(got, `"kind":"circuit_breaker_open"`) || !strings.Contains(got, `"retry_after_ms":2000`) {
		t.Fatalf("expected circuit breaker alert with 2s backoff, got: %s", got)
	}

	if err := safety.before(endpoint); err == nil {
		t.Fatal("open breaker should block new requests")
	} else if !strings.Contains(err.Error(), `"kind":"circuit_breaker_open"`) {
		t.Fatalf("expected circuit breaker block, got: %s", err.Error())
	}

	now = now.Add(2 * time.Second)
	if err := safety.before(endpoint); err != nil {
		t.Fatalf("breaker should allow request after backoff: %v", err)
	}
	_ = safety.after(endpoint, false, nil)
	if err := safety.before(endpoint); err != nil {
		t.Fatalf("successful response should reset remote-rate-limit streak: %v", err)
	}
}

func TestExternalAPISafetyDefaultsAndAlertFormatting(t *testing.T) {
	safety := newExternalAPISafety(nil, externalAPISafetyPolicy{})
	if safety.policy.RatePerSecond != 1 {
		t.Fatalf("default rate = %v, want 1", safety.policy.RatePerSecond)
	}
	if safety.policy.Burst != 1 {
		t.Fatalf("default burst = %d, want 1", safety.policy.Burst)
	}
	if safety.policy.BreakerThreshold != 3 {
		t.Fatalf("default breaker threshold = %d, want 3", safety.policy.BreakerThreshold)
	}
	if safety.policy.BreakerBaseBackoff != time.Second {
		t.Fatalf("default breaker base backoff = %s, want 1s", safety.policy.BreakerBaseBackoff)
	}
	if safety.policy.BreakerMaxBackoff != time.Second {
		t.Fatalf("default breaker max backoff = %s, want 1s", safety.policy.BreakerMaxBackoff)
	}
	if safety.policy.MaxConsecutiveShiftPower != 10 {
		t.Fatalf("default shift cap = %d, want 10", safety.policy.MaxConsecutiveShiftPower)
	}
	if got := normalizeExternalEndpoint(" "); got != "external:unknown" {
		t.Fatalf("blank endpoint normalized to %q", got)
	}
	err := newToolSafetyError(toolSafetyAlert{Kind: "rate_limit_exceeded", Endpoint: "e", Message: "blocked"}, nil)
	if got := err.Error(); !strings.Contains(got, `"kind":"rate_limit_exceeded"`) || strings.Contains(got, `"error"`) {
		t.Fatalf("unexpected alert JSON: %s", got)
	}
}

func TestGuardedExternalHTTPGetBodyRecordsSuccessAndNonRateStatus(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	replaceDefaultExternalAPISafetyForTest(t, newExternalAPISafety(func() time.Time { return now }, externalAPISafetyPolicy{
		RatePerSecond:      10,
		Burst:              10,
		BreakerThreshold:   3,
		BreakerBaseBackoff: time.Second,
		BreakerMaxBackoff:  time.Minute,
	}))
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			status := http.StatusOK
			body := "ok"
			if strings.Contains(req.URL.Path, "server-error") {
				status = http.StatusInternalServerError
				body = "try later"
			}
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	got, err := guardedExternalHTTPGetBody(client, "test:success", "https://example.com/success")
	if err != nil {
		t.Fatalf("success request failed: %v", err)
	}
	if got.Status != http.StatusOK || string(got.Body) != "ok" {
		t.Fatalf("success response = %+v body=%q", got, string(got.Body))
	}
	got, err = guardedExternalHTTPGetBody(client, "test:server-error", "https://example.com/server-error")
	if err != nil {
		t.Fatalf("non-rate HTTP status should be returned for caller handling, got: %v", err)
	}
	if got.Status != http.StatusInternalServerError || string(got.Body) != "try later" {
		t.Fatalf("server-error response = %+v body=%q", got, string(got.Body))
	}
}

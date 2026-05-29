package tools

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func replaceATProtoResolverHTTPClientForTest(t *testing.T, client *http.Client) {
	t.Helper()
	old := atprotoResolverHTTPClient
	atprotoResolverHTTPClient = client
	t.Cleanup(func() {
		atprotoResolverHTTPClient = old
	})
}

func TestResolveHandleToDIDRateLimitStopsWithSafetyAlert(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	replaceDefaultExternalAPISafetyForTest(t, newExternalAPISafety(func() time.Time { return now }, externalAPISafetyPolicy{
		RatePerSecond:      100,
		Burst:              100,
		BreakerThreshold:   3,
		BreakerBaseBackoff: time.Second,
		BreakerMaxBackoff:  time.Minute,
	}))
	requests := 0
	replaceATProtoResolverHTTPClientForTest(t, &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"RateLimitExceeded"}`)),
				Request:    req,
			}, nil
		}),
	})

	_, err := resolveHandleToDID("alice.example")
	if err == nil {
		t.Fatal("expected resolver rate limit to fail")
	}
	var safetyErr *toolSafetyError
	if !errors.As(err, &safetyErr) {
		t.Fatalf("expected toolSafetyError, got %T: %v", err, err)
	}
	if safetyErr.alert.Kind != "remote_rate_limit" {
		t.Fatalf("alert kind = %q, want remote_rate_limit", safetyErr.alert.Kind)
	}
	if requests != 1 {
		t.Fatalf("rate-limited appview resolver should stop before fallback requests, got %d requests", requests)
	}
}

package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxExternalItemsPerCall = 100

type externalAPISafetyPolicy struct {
	RatePerSecond            float64
	Burst                    int
	BreakerThreshold         int
	BreakerBaseBackoff       time.Duration
	BreakerMaxBackoff        time.Duration
	MaxConsecutiveShiftPower int
}

type externalAPISafety struct {
	mu        sync.Mutex
	now       func() time.Time
	policy    externalAPISafetyPolicy
	endpoints map[string]*externalEndpointSafety
}

type externalEndpointSafety struct {
	tokens                     float64
	updatedAt                  time.Time
	consecutiveRateLimitErrors int
	backoffUntil               time.Time
}

type toolSafetyError struct {
	alert toolSafetyAlert
	cause error
}

type toolSafetyAlert struct {
	Kind                       string `json:"kind"`
	Endpoint                   string `json:"endpoint"`
	Message                    string `json:"message"`
	RetryAfterMS               int64  `json:"retry_after_ms,omitempty"`
	BackoffUntil               string `json:"backoff_until,omitempty"`
	ConsecutiveRateLimitErrors int    `json:"consecutive_rate_limit_errors,omitempty"`
}

var defaultExternalAPISafety = newExternalAPISafety(time.Now, externalAPISafetyPolicy{
	RatePerSecond:            5,
	Burst:                    100,
	BreakerThreshold:         3,
	BreakerBaseBackoff:       30 * time.Second,
	BreakerMaxBackoff:        15 * time.Minute,
	MaxConsecutiveShiftPower: 10,
})

func clampExternalPageLimit(requested, defaultLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxExternalItemsPerCall {
		return maxExternalItemsPerCall
	}
	return requested
}

func newExternalAPISafety(now func() time.Time, policy externalAPISafetyPolicy) *externalAPISafety {
	if now == nil {
		now = time.Now
	}
	if policy.RatePerSecond <= 0 {
		policy.RatePerSecond = 1
	}
	if policy.Burst <= 0 {
		policy.Burst = 1
	}
	if policy.BreakerThreshold <= 0 {
		policy.BreakerThreshold = 3
	}
	if policy.BreakerBaseBackoff <= 0 {
		policy.BreakerBaseBackoff = time.Second
	}
	if policy.BreakerMaxBackoff <= 0 {
		policy.BreakerMaxBackoff = policy.BreakerBaseBackoff
	}
	if policy.MaxConsecutiveShiftPower <= 0 {
		policy.MaxConsecutiveShiftPower = 10
	}
	return &externalAPISafety{
		now:       now,
		policy:    policy,
		endpoints: make(map[string]*externalEndpointSafety),
	}
}

func (s *externalAPISafety) before(endpoint string) *toolSafetyError {
	if s == nil {
		return nil
	}
	endpoint = normalizeExternalEndpoint(endpoint)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.endpointLocked(endpoint, now)
	s.refillLocked(state, now)

	if state.backoffUntil.After(now) {
		return newToolSafetyError(toolSafetyAlert{
			Kind:                       "circuit_breaker_open",
			Endpoint:                   endpoint,
			Message:                    "external API circuit breaker is open after repeated rate-limit responses",
			RetryAfterMS:               int64(math.Ceil(float64(state.backoffUntil.Sub(now)) / float64(time.Millisecond))),
			BackoffUntil:               state.backoffUntil.UTC().Format(time.RFC3339),
			ConsecutiveRateLimitErrors: state.consecutiveRateLimitErrors,
		}, nil)
	}

	if state.tokens < 1 {
		wait := time.Duration(math.Ceil((1-state.tokens)/s.policy.RatePerSecond*1000)) * time.Millisecond
		return newToolSafetyError(toolSafetyAlert{
			Kind:         "rate_limit_exceeded",
			Endpoint:     endpoint,
			Message:      "external API endpoint rate limit exceeded before request",
			RetryAfterMS: int64(wait / time.Millisecond),
		}, nil)
	}

	state.tokens--
	return nil
}

func (s *externalAPISafety) after(endpoint string, rateLimited bool, cause error) *toolSafetyError {
	if s == nil {
		return nil
	}
	endpoint = normalizeExternalEndpoint(endpoint)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.endpointLocked(endpoint, now)
	if !rateLimited {
		state.consecutiveRateLimitErrors = 0
		return nil
	}

	state.consecutiveRateLimitErrors++
	alert := toolSafetyAlert{
		Kind:                       "remote_rate_limit",
		Endpoint:                   endpoint,
		Message:                    "external API returned a rate-limit response",
		ConsecutiveRateLimitErrors: state.consecutiveRateLimitErrors,
	}

	if state.consecutiveRateLimitErrors >= s.policy.BreakerThreshold {
		shift := state.consecutiveRateLimitErrors - s.policy.BreakerThreshold
		if shift > s.policy.MaxConsecutiveShiftPower {
			shift = s.policy.MaxConsecutiveShiftPower
		}
		backoff := s.policy.BreakerBaseBackoff * time.Duration(1<<shift)
		if backoff > s.policy.BreakerMaxBackoff {
			backoff = s.policy.BreakerMaxBackoff
		}
		state.backoffUntil = now.Add(backoff)
		alert.Kind = "circuit_breaker_open"
		alert.Message = "external API circuit breaker opened after repeated rate-limit responses"
		alert.RetryAfterMS = int64(backoff / time.Millisecond)
		alert.BackoffUntil = state.backoffUntil.UTC().Format(time.RFC3339)
	}

	return newToolSafetyError(alert, cause)
}

func (s *externalAPISafety) endpointLocked(endpoint string, now time.Time) *externalEndpointSafety {
	state := s.endpoints[endpoint]
	if state == nil {
		state = &externalEndpointSafety{tokens: float64(s.policy.Burst), updatedAt: now}
		s.endpoints[endpoint] = state
	}
	return state
}

func (s *externalAPISafety) refillLocked(state *externalEndpointSafety, now time.Time) {
	elapsed := now.Sub(state.updatedAt).Seconds()
	if elapsed > 0 {
		state.tokens += elapsed * s.policy.RatePerSecond
		if burst := float64(s.policy.Burst); state.tokens > burst {
			state.tokens = burst
		}
		state.updatedAt = now
	}
}

func normalizeExternalEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "external:unknown"
	}
	return endpoint
}

func newToolSafetyError(alert toolSafetyAlert, cause error) *toolSafetyError {
	return &toolSafetyError{alert: alert, cause: cause}
}

func toolSafetyErrorOutput(err error) (string, bool) {
	var safetyErr *toolSafetyError
	if errors.As(err, &safetyErr) {
		return safetyErr.Error(), true
	}
	return "", false
}

func (e *toolSafetyError) Error() string {
	payload := map[string]any{
		"ok":    false,
		"alert": e.alert,
	}
	if e.cause != nil {
		payload["error"] = e.cause.Error()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		if e.cause != nil {
			return fmt.Sprintf("%s: %s", e.alert.Message, e.cause)
		}
		return e.alert.Message
	}
	return string(body)
}

func atprotoSafetyEndpoint(nsid string) string {
	return "atproto:" + strings.TrimSpace(nsid)
}

func newsSafetyEndpoint(rawURL string) string {
	return "news_fetch:" + strings.TrimSpace(rawURL)
}

func isHTTPRateLimitStatus(status int) bool {
	return status == http.StatusTooManyRequests
}

type guardedHTTPBody struct {
	Status int
	Body   []byte
}

func guardedExternalHTTPGetBody(client *http.Client, endpoint, rawURL string) (guardedHTTPBody, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if safetyErr := defaultExternalAPISafety.before(endpoint); safetyErr != nil {
		return guardedHTTPBody{}, safetyErr
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		_ = defaultExternalAPISafety.after(endpoint, false, nil)
		return guardedHTTPBody{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = defaultExternalAPISafety.after(endpoint, false, nil)
		return guardedHTTPBody{Status: resp.StatusCode}, err
	}
	out := guardedHTTPBody{Status: resp.StatusCode, Body: data}
	if resp.StatusCode != http.StatusOK {
		cause := fmt.Errorf("external GET %s (%d): %s", endpoint, resp.StatusCode, string(data))
		if safetyErr := defaultExternalAPISafety.after(endpoint, isHTTPRateLimitStatus(resp.StatusCode), cause); safetyErr != nil {
			return out, safetyErr
		}
		return out, nil
	}
	_ = defaultExternalAPISafety.after(endpoint, false, nil)
	return out, nil
}

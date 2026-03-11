package providers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

// RetryConfig controls retry behaviour for the RetryProvider middleware.
type RetryConfig struct {
	MaxAttempts int           // default 3
	BaseDelay   time.Duration // default 1s
	MaxDelay    time.Duration // default 30s
	JitterFrac  float64       // default 0.25
	// OnRetry is called before each retry sleep. If nil, a default message is printed to stderr.
	OnRetry func(attempt int, delay time.Duration, statusCode int)
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
		JitterFrac:  0.25,
	}
}

// RetryProvider wraps a Provider and retries on RetryableError.
type RetryProvider struct {
	inner  Provider
	config RetryConfig
}

// WithRetry wraps a Provider with retry logic. If inner also implements
// Streamer, the returned Provider will implement Streamer as well.
func WithRetry(p Provider, cfg RetryConfig) Provider {
	cfg = normalizeRetryConfig(cfg)
	rp := &RetryProvider{inner: p, config: cfg}
	if _, ok := p.(Streamer); ok {
		return &retryStreamer{RetryProvider: rp}
	}
	return rp
}

func normalizeRetryConfig(cfg RetryConfig) RetryConfig {
	defaults := DefaultRetryConfig()
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaults.MaxAttempts
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = defaults.BaseDelay
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = defaults.MaxDelay
	}
	if cfg.JitterFrac < 0 {
		cfg.JitterFrac = 0
	}
	return cfg
}

func (rp *RetryProvider) Name() string               { return rp.inner.Name() }
func (rp *RetryProvider) Capabilities() Capabilities { return rp.inner.Capabilities() }

func (rp *RetryProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	return retryCall(ctx, rp.config, func() (EmbedResponse, error) {
		return rp.inner.Embed(ctx, req)
	})
}

func (rp *RetryProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	return rp.inner.Metadata(ctx, model)
}

func (rp *RetryProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	return retryCall(ctx, rp.config, func() (CompleteResponse, error) {
		return rp.inner.Complete(ctx, req)
	})
}

func retryCall[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		resp, err := fn()
		if err == nil {
			return resp, nil
		}

		var re *RetryableError
		if !errors.As(err, &re) || !isRetryableStatus(re.StatusCode) {
			return zero, err
		}
		lastErr = err

		if attempt == cfg.MaxAttempts-1 {
			break
		}

		delay := backoffDelay(cfg, attempt, re.RetryAfter)
		if cfg.OnRetry != nil {
			cfg.OnRetry(attempt, delay, re.StatusCode)
		} else {
			fmt.Fprintf(os.Stderr, "⏳ rate limited (HTTP %d) — retrying in %s (attempt %d/%d)...\n",
				re.StatusCode, delay.Round(time.Second), attempt+1, cfg.MaxAttempts-1)
		}
		if err := waitForRetry(ctx, delay); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
}

// retryStreamer adds the Streamer interface when the inner provider supports it.
type retryStreamer struct {
	*RetryProvider
}

func (rs *retryStreamer) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	s := rs.inner.(Streamer)
	return retryCall(ctx, rs.config, func() (<-chan StreamEvent, error) {
		return s.StreamComplete(ctx, req)
	})
}

func (rs *retryStreamer) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	return rs.RetryProvider.Metadata(ctx, model)
}

func isRetryableStatus(code int) bool {
	return code == 429 || (code >= 500 && code < 600)
}

func backoffDelay(cfg RetryConfig, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := float64(cfg.BaseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}
	if cfg.JitterFrac > 0 {
		delay += delay * cfg.JitterFrac * (rand.Float64()*2 - 1) //nolint:gosec
	}
	if delay < 0 {
		delay = 0
	}
	return time.Duration(delay)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryAfterFromHeader(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

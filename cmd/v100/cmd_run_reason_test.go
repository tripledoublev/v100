package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

func TestClassifyProviderFailureReason(t *testing.T) {
	rateLimit := &providers.RetryableError{Err: errors.New("quota"), StatusCode: 429}
	if got := classifyProviderFailureReason(rateLimit); got != "rate_limit_exhausted" {
		t.Fatalf("classifyProviderFailureReason(rateLimit) = %q", got)
	}

	retryBudget := &providers.RetryBudgetExceededError{
		LastErr: &providers.RetryableError{Err: errors.New("quota"), StatusCode: 429},
		Waited:  30 * time.Second,
		MaxWait: 90 * time.Second,
	}
	if got := classifyProviderFailureReason(retryBudget); got != "rate_limit_exhausted" {
		t.Fatalf("classifyProviderFailureReason(retryBudget) = %q", got)
	}

	otherRetryBudget := &providers.RetryBudgetExceededError{
		LastErr: &providers.RetryableError{Err: errors.New("server"), StatusCode: 503},
		Waited:  30 * time.Second,
		MaxWait: 90 * time.Second,
	}
	if got := classifyProviderFailureReason(otherRetryBudget); got != "retry_budget_exhausted" {
		t.Fatalf("classifyProviderFailureReason(otherRetryBudget) = %q", got)
	}
}

func TestFormatRetryBudgetErrorIncludesMaxWait(t *testing.T) {
	err := &providers.RetryBudgetExceededError{
		LastErr: &providers.RetryableError{Err: errors.New("quota"), StatusCode: 429},
		Waited:  46 * time.Second,
		MaxWait: 90 * time.Second,
	}
	got := formatRetryBudgetError(err)
	for _, want := range []string{"retry budget exhausted", "HTTP 429", "max wait 1m30s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRetryBudgetError() = %q, missing %q", got, want)
		}
	}
}

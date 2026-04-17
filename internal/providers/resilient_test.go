package providers

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

// resilientStubProvider is a minimal Provider used to exercise ResilientProvider.
type resilientStubProvider struct {
	name     string
	reply    string
	err      error
	calls    int
	metadata ModelMetadata
}

func (s *resilientStubProvider) Name() string                 { return s.name }
func (s *resilientStubProvider) Capabilities() Capabilities   { return Capabilities{} }
func (s *resilientStubProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	return s.metadata, nil
}
func (s *resilientStubProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	s.calls++
	if s.err != nil {
		return EmbedResponse{}, s.err
	}
	return EmbedResponse{}, nil
}
func (s *resilientStubProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	s.calls++
	if s.err != nil {
		return CompleteResponse{}, s.err
	}
	return CompleteResponse{AssistantText: s.reply}, nil
}

func silenceLogs(t *testing.T) {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

func TestResilientProvider_PrimaryOK(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", reply: "hello"}
	rp := NewResilientProvider(primary, nil)
	resp, err := rp.Complete(context.Background(), CompleteRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.AssistantText != "hello" {
		t.Fatalf("got %q", resp.AssistantText)
	}
	if primary.calls != 1 {
		t.Fatalf("primary calls=%d, want 1", primary.calls)
	}
}

func TestResilientProvider_FallbackOnError(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", err: errors.New("primary down")}
	fb := &resilientStubProvider{name: "b", reply: "from-b"}
	rp := NewResilientProvider(primary, []Provider{fb})

	resp, err := rp.Complete(context.Background(), CompleteRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.AssistantText != "from-b" {
		t.Fatalf("got %q, want from-b", resp.AssistantText)
	}
	if primary.calls != 1 || fb.calls != 1 {
		t.Fatalf("calls primary=%d fb=%d, want 1/1", primary.calls, fb.calls)
	}
}

func TestResilientProvider_AllFail(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", err: errors.New("a down")}
	fb := &resilientStubProvider{name: "b", err: errors.New("b down")}
	rp := NewResilientProvider(primary, []Provider{fb})

	_, err := rp.Complete(context.Background(), CompleteRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all") || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error should mention failure chain, got: %v", err)
	}
}

// Once the primary trips its unhealthy threshold, it should be skipped in
// favor of a healthy fallback on subsequent calls.
func TestResilientProvider_SkipsUnhealthyPrimary(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", err: errors.New("a down")}
	fb := &resilientStubProvider{name: "b", reply: "ok"}
	rp := NewResilientProvider(primary, []Provider{fb})

	// Drive the primary past maxErr so it becomes unhealthy.
	for range rp.Health[primary.Name()].maxErr {
		_, _ = rp.Complete(context.Background(), CompleteRequest{})
	}
	beforePrimaryCalls := primary.calls

	// Next call should short-circuit primary.
	if _, err := rp.Complete(context.Background(), CompleteRequest{}); err != nil {
		t.Fatalf("unexpected err on healthy fallback: %v", err)
	}
	if primary.calls != beforePrimaryCalls {
		t.Fatalf("primary should be skipped while unhealthy (calls went %d → %d)",
			beforePrimaryCalls, primary.calls)
	}
}

// WithRetry wraps providers in *RetryProvider; health reporting must still
// be reachable from the outside so `v100 providers health` works.
func TestRetryProvider_ForwardsHealthStatus(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", reply: "ok"}
	rp := NewResilientProvider(primary, nil)

	wrapped := WithRetry(rp, DefaultRetryConfig())
	hr, ok := wrapped.(interface{ HealthStatus() []HealthStatus })
	if !ok {
		t.Fatal("WithRetry output should expose HealthStatus via interface assertion")
	}
	if _, err := wrapped.Complete(context.Background(), CompleteRequest{}); err != nil {
		t.Fatalf("complete err: %v", err)
	}
	statuses := hr.HealthStatus()
	if len(statuses) != 1 || statuses[0].Provider != "a" || statuses[0].TotalCalls != 1 {
		t.Fatalf("HealthStatus not forwarded through retry: %+v", statuses)
	}
}

func TestResilientProvider_HealthStatusReportsAll(t *testing.T) {
	silenceLogs(t)
	primary := &resilientStubProvider{name: "a", reply: "ok"}
	fb := &resilientStubProvider{name: "b", reply: "ok"}
	rp := NewResilientProvider(primary, []Provider{fb})

	_, _ = rp.Complete(context.Background(), CompleteRequest{})
	statuses := rp.HealthStatus()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	if statuses[0].Provider != "a" || statuses[1].Provider != "b" {
		t.Fatalf("order wrong: %+v", statuses)
	}
}

package providers

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type stubProvider struct {
	name             string
	caps             Capabilities
	completeFn       func(context.Context, CompleteRequest) (CompleteResponse, error)
	embedFn          func(context.Context, EmbedRequest) (EmbedResponse, error)
	streamCompleteFn func(context.Context, CompleteRequest) (<-chan StreamEvent, error)
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) Capabilities() Capabilities { return p.caps }

func (p *stubProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	if p.completeFn != nil {
		return p.completeFn(ctx, req)
	}
	return CompleteResponse{}, nil
}

func (p *stubProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if p.embedFn != nil {
		return p.embedFn(ctx, req)
	}
	return EmbedResponse{}, nil
}

func (p *stubProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	if p.streamCompleteFn != nil {
		return p.streamCompleteFn(ctx, req)
	}
	return nil, io.EOF
}

func (p *stubProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	return ModelMetadata{Model: p.name, ContextSize: 4096}, nil
}

func testRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
		JitterFrac:  0,
	}
}

func TestRetryProviderSuccessOnFirstAttempt(t *testing.T) {
	attempts := 0
	p := &stubProvider{
		name: "stub",
		completeFn: func(context.Context, CompleteRequest) (CompleteResponse, error) {
			attempts++
			return CompleteResponse{AssistantText: "ok"}, nil
		},
	}

	rp := WithRetry(p, testRetryConfig())
	resp, err := rp.Complete(context.Background(), CompleteRequest{})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if resp.AssistantText != "ok" {
		t.Fatalf("assistant text = %q, want ok", resp.AssistantText)
	}
}

func TestRetryProviderSuccessOnSecondAttempt(t *testing.T) {
	attempts := 0
	p := &stubProvider{
		name: "stub",
		completeFn: func(context.Context, CompleteRequest) (CompleteResponse, error) {
			attempts++
			if attempts == 1 {
				return CompleteResponse{}, &RetryableError{
					Err:        errors.New("temporary"),
					StatusCode: 503,
				}
			}
			return CompleteResponse{AssistantText: "recovered"}, nil
		},
	}

	rp := WithRetry(p, testRetryConfig())
	resp, err := rp.Complete(context.Background(), CompleteRequest{})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if resp.AssistantText != "recovered" {
		t.Fatalf("assistant text = %q, want recovered", resp.AssistantText)
	}
}

func TestRetryProviderExhaustedRetriesReturnsLastError(t *testing.T) {
	attempts := 0
	last := &RetryableError{Err: errors.New("still failing"), StatusCode: 429}
	p := &stubProvider{
		name: "stub",
		completeFn: func(context.Context, CompleteRequest) (CompleteResponse, error) {
			attempts++
			return CompleteResponse{}, last
		},
	}

	rp := WithRetry(p, testRetryConfig())
	_, err := rp.Complete(context.Background(), CompleteRequest{})
	if !errors.Is(err, last) {
		t.Fatalf("Complete error = %v, want wrapped last error %v", err, last)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryProviderNonRetryableFailsImmediately(t *testing.T) {
	attempts := 0
	wantErr := errors.New("bad request")
	p := &stubProvider{
		name: "stub",
		completeFn: func(context.Context, CompleteRequest) (CompleteResponse, error) {
			attempts++
			return CompleteResponse{}, wantErr
		},
	}

	rp := WithRetry(p, testRetryConfig())
	_, err := rp.Complete(context.Background(), CompleteRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Complete error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryProviderContextCancellationStopsRetry(t *testing.T) {
	attempts := 0
	p := &stubProvider{
		name: "stub",
		completeFn: func(context.Context, CompleteRequest) (CompleteResponse, error) {
			attempts++
			return CompleteResponse{}, &RetryableError{
				Err:        errors.New("temporary"),
				StatusCode: 503,
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rp := WithRetry(p, testRetryConfig())
	_, err := rp.Complete(ctx, CompleteRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete error = %v, want context canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryProviderForwardsStreamer(t *testing.T) {
	attempts := 0
	p := &stubProvider{
		name: "stub",
		streamCompleteFn: func(context.Context, CompleteRequest) (<-chan StreamEvent, error) {
			attempts++
			if attempts == 1 {
				return nil, &RetryableError{
					Err:        errors.New("temporary"),
					StatusCode: 503,
				}
			}
			ch := make(chan StreamEvent, 1)
			ch <- StreamEvent{Type: StreamDone}
			close(ch)
			return ch, nil
		},
	}

	rp := WithRetry(p, testRetryConfig())
	streamer, ok := rp.(Streamer)
	if !ok {
		t.Fatal("wrapped provider should implement Streamer")
	}

	ch, err := streamer.StreamComplete(context.Background(), CompleteRequest{})
	if err != nil {
		t.Fatalf("StreamComplete returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	ev, ok := <-ch
	if !ok {
		t.Fatal("expected streamed event")
	}
	if ev.Type != StreamDone {
		t.Fatalf("event type = %v, want %v", ev.Type, StreamDone)
	}
}

package providers

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ResilientProvider wraps a primary provider with automatic fallback to
// alternate providers on retry exhaustion. It tracks health per provider
// and routes around degraded endpoints.
type ResilientProvider struct {
	Primary   Provider
	Fallbacks []Provider
	Health    map[string]*HealthTracker

	mu sync.Mutex
}

// NewResilientProvider creates a provider that falls through to fallbacks
// when the primary is unhealthy or retries are exhausted.
func NewResilientProvider(primary Provider, fallbacks []Provider) *ResilientProvider {
	rp := &ResilientProvider{
		Primary:   primary,
		Fallbacks: fallbacks,
		Health:    make(map[string]*HealthTracker),
	}
	rp.Health[primary.Name()] = NewHealthTracker(primary.Name())
	for _, fb := range fallbacks {
		rp.Health[fb.Name()] = NewHealthTracker(fb.Name())
	}
	return rp
}

func (rp *ResilientProvider) chain() []Provider {
	var chain []Provider
	if rp.Primary != nil {
		if rp.Health[rp.Primary.Name()].IsHealthy() {
			chain = append(chain, rp.Primary)
		}
	}
	for _, fb := range rp.Fallbacks {
		if rp.Health[fb.Name()].IsHealthy() {
			chain = append(chain, fb)
		}
	}
	if len(chain) == 0 && rp.Primary != nil {
		chain = append(chain, rp.Primary)
	}
	return chain
}

func (rp *ResilientProvider) Name() string { return rp.Primary.Name() }

func (rp *ResilientProvider) Capabilities() Capabilities { return rp.Primary.Capabilities() }

func (rp *ResilientProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	chain := rp.chain()
	var lastErr error
	for i, p := range chain {
		start := time.Now()
		resp, err := p.Complete(ctx, req)
		latency := time.Since(start)
		if err == nil {
			rp.Health[p.Name()].RecordSuccess(latency)
			if i > 0 {
				log.Printf("[resilient] %q fallback to %q succeeded (attempt %d/%d)", rp.Primary.Name(), p.Name(), i+1, len(chain))
			}
			return resp, nil
		}
		rp.Health[p.Name()].RecordError(err, latency)
		lastErr = err
		if i < len(chain)-1 {
			log.Printf("[resilient] %q failed (%v), falling through to %q", p.Name(), err, chain[i+1].Name())
		}
	}
	return CompleteResponse{}, fmt.Errorf("resilient: all %d providers failed (primary=%q): %w", len(chain), rp.Primary.Name(), lastErr)
}

func (rp *ResilientProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	chain := rp.chain()
	var lastErr error
	for i, p := range chain {
		s, ok := p.(Streamer)
		if !ok {
			continue
		}
		start := time.Now()
		ch, err := s.StreamComplete(ctx, req)
		latency := time.Since(start)
		if err != nil {
			rp.Health[p.Name()].RecordError(err, latency)
			lastErr = err
			if i < len(chain)-1 {
				log.Printf("[resilient] %q stream failed (%v), falling through to %q", p.Name(), err, chain[i+1].Name())
			}
			continue
		}
		rp.Health[p.Name()].RecordSuccess(latency)
		if i > 0 {
			log.Printf("[resilient] %q stream fallback to %q succeeded", rp.Primary.Name(), p.Name())
		}
		return ch, nil
	}
	return nil, fmt.Errorf("resilient: all %d providers failed for stream: %w", len(chain), lastErr)
}

func (rp *ResilientProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	chain := rp.chain()
	var lastErr error
	for _, p := range chain {
		resp, err := p.Embed(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return EmbedResponse{}, fmt.Errorf("resilient: all providers failed for embed: %w", lastErr)
}

func (rp *ResilientProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	return rp.Primary.Metadata(ctx, model)
}

// HealthStatus returns health summaries for all providers in the chain.
func (rp *ResilientProvider) HealthStatus() []HealthStatus {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	var statuses []HealthStatus
	if rp.Primary != nil {
		if ht, ok := rp.Health[rp.Primary.Name()]; ok {
			statuses = append(statuses, ht.Status())
		}
	}
	for _, fb := range rp.Fallbacks {
		if ht, ok := rp.Health[fb.Name()]; ok {
			statuses = append(statuses, ht.Status())
		}
	}
	return statuses
}

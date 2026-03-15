package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

type fakeSummaryProvider struct {
	summaryText   string
	completeCalls int
}

func (f *fakeSummaryProvider) Name() string { return "fake" }

func (f *fakeSummaryProvider) Capabilities() providers.Capabilities { return providers.Capabilities{} }

func (f *fakeSummaryProvider) Complete(_ context.Context, _ providers.CompleteRequest) (providers.CompleteResponse, error) {
	f.completeCalls++
	return providers.CompleteResponse{AssistantText: f.summaryText}, nil
}

func (f *fakeSummaryProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}

func (f *fakeSummaryProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

func TestEmitFinalTUIRunEndIncludesSummaryWhenHealthy(t *testing.T) {
	t.Helper()

	var got core.Event
	trace, err := core.OpenTrace(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = trace.Close() }()

	loop := &core.Loop{
		Run:      &core.Run{ID: "run-test"},
		Trace:    trace,
		Budget:   core.NewBudgetTracker(&core.Budget{}),
		Messages: []providers.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "done"}},
		OutputFn: func(ev core.Event) {
			got = ev
		},
	}
	prov := &fakeSummaryProvider{summaryText: "finished cleanly"}

	if err := emitFinalTUIRunEnd(loop, prov, "model-x", "budget_exceeded"); err != nil {
		t.Fatalf("emitFinalTUIRunEnd: %v", err)
	}

	if prov.completeCalls != 1 {
		t.Fatalf("expected one summary call, got %d", prov.completeCalls)
	}
	if got.Type != core.EventRunEnd {
		t.Fatalf("expected run.end event, got %v", got.Type)
	}

	var payload core.RunEndPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Reason != "budget_exceeded" {
		t.Fatalf("unexpected reason: %q", payload.Reason)
	}
	if payload.Summary != "finished cleanly" {
		t.Fatalf("unexpected summary: %q", payload.Summary)
	}
}

func TestEmitFinalTUIRunEndSkipsSummaryOnProviderError(t *testing.T) {
	t.Helper()

	var got core.Event
	trace, err := core.OpenTrace(filepath.Join(t.TempDir(), "trace.jsonl"))
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = trace.Close() }()

	loop := &core.Loop{
		Run:      &core.Run{ID: "run-test"},
		Trace:    trace,
		Budget:   core.NewBudgetTracker(&core.Budget{}),
		Messages: []providers.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "partial"}},
		OutputFn: func(ev core.Event) {
			got = ev
		},
	}
	prov := &fakeSummaryProvider{summaryText: "should not be used"}

	if err := emitFinalTUIRunEnd(loop, prov, "model-x", "error"); err != nil {
		t.Fatalf("emitFinalTUIRunEnd: %v", err)
	}

	if prov.completeCalls != 0 {
		t.Fatalf("expected summary to be skipped, got %d Complete calls", prov.completeCalls)
	}

	var payload core.RunEndPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Reason != "error" {
		t.Fatalf("unexpected reason: %q", payload.Reason)
	}
	if payload.Summary != "" {
		t.Fatalf("expected empty summary, got %q", payload.Summary)
	}
}

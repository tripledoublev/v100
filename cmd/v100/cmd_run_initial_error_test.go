package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

type initialFailureProvider struct {
	err error
}

func (p *initialFailureProvider) Name() string { return "initial-failure" }

func (p *initialFailureProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}

func (p *initialFailureProvider) Complete(context.Context, providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{}, p.err
}

func (p *initialFailureProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}

func (p *initialFailureProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: "model-x", ContextSize: 4096}, nil
}

func TestRunWithCLIExitInitialPromptFailureEndsError(t *testing.T) {
	runDir := t.TempDir()
	workspace := t.TempDir()
	tracePath := filepath.Join(runDir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = trace.Close() }()

	run := &core.Run{
		ID:        "run-initial-error",
		Dir:       workspace,
		StateDir:  filepath.Join(runDir, "state"),
		TraceFile: tracePath,
		Budget:    core.Budget{MaxSteps: 10, MaxTokens: 10000},
	}
	budget := core.NewBudgetTracker(&run.Budget)
	prov := &initialFailureProvider{err: errors.New("provider stream: codex: HTTP 400: unsupported parameter")}

	err = runWithCLI(
		config.DefaultConfig(),
		run,
		prov,
		nil,
		tools.NewRegistry(nil),
		policy.Default(),
		trace,
		budget,
		"model-x",
		"never",
		workspace,
		false,
		true,
		false,
		false,
		false,
		providers.GenParams{},
		&core.ReactSolver{},
		"hello",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runWithCLI returned error: %v", err)
	}

	events, err := core.ReadAll(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var gotReason string
	for _, ev := range events {
		if ev.Type != core.EventRunEnd {
			continue
		}
		var payload core.RunEndPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("unmarshal run.end: %v", err)
		}
		gotReason = payload.Reason
	}
	if gotReason != "error" {
		t.Fatalf("run.end reason = %q, want error", gotReason)
	}
}

func TestRunWithCLIExitExactStepBudgetEndsPromptExit(t *testing.T) {
	runDir := t.TempDir()
	workspace := t.TempDir()
	tracePath := filepath.Join(runDir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer func() { _ = trace.Close() }()

	run := &core.Run{
		ID:        "run-exact-step-budget",
		Dir:       workspace,
		StateDir:  filepath.Join(runDir, "state"),
		TraceFile: tracePath,
		Budget:    core.Budget{MaxSteps: 1, MaxTokens: 10000},
	}
	budget := core.NewBudgetTracker(&run.Budget)

	err = runWithCLI(
		config.DefaultConfig(),
		run,
		&fakeSummaryProvider{summaryText: "finished cleanly"},
		nil,
		tools.NewRegistry(nil),
		policy.Default(),
		trace,
		budget,
		"model-x",
		"never",
		workspace,
		false,
		true,
		false,
		false,
		false,
		providers.GenParams{},
		&core.ReactSolver{},
		"hello",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("runWithCLI returned error: %v", err)
	}

	events, err := core.ReadAll(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var gotReason string
	for _, ev := range events {
		if ev.Type != core.EventRunEnd {
			continue
		}
		var payload core.RunEndPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("unmarshal run.end: %v", err)
		}
		gotReason = payload.Reason
	}
	if gotReason != "prompt_exit" {
		t.Fatalf("run.end reason = %q, want prompt_exit", gotReason)
	}
}

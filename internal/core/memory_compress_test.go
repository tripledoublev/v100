package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

// capturingProvider records every CompleteRequest it receives alongside its response.
type capturingProvider struct {
	responses []providers.CompleteResponse
	requests  []providers.CompleteRequest
}

func (c *capturingProvider) Name() string { return "capturing" }
func (c *capturingProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{ToolCalls: true}
}
func (c *capturingProvider) Complete(_ context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	c.requests = append(c.requests, req)
	idx := len(c.requests) - 1
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	return providers.CompleteResponse{AssistantText: "done"}, nil
}

func (c *capturingProvider) Embed(_ context.Context, _ providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{Embedding: []float32{0.1, 0.2}}, nil
}

func (c *capturingProvider) Metadata(_ context.Context, model string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{Model: "mock", ContextSize: 4096}, nil
}

// newCompressLoop builds a Loop wired for compression tests.
func newCompressLoop(t *testing.T, prov providers.Provider, contextLimit int) *core.Loop {
	t.Helper()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = trace.Close() })

	pol := policy.Default()
	pol.ContextLimit = contextLimit

	return &core.Loop{
		Run:       &core.Run{ID: "t", Dir: dir, TraceFile: tracePath},
		Provider:  prov,
		Tools:     tools.NewRegistry(nil),
		Policy:    pol,
		Trace:     trace,
		Budget:    core.NewBudgetTracker(&core.Budget{MaxSteps: 50, MaxTokens: 1_000_000}),
		ConfirmFn: func(_, _ string) bool { return true },
		Mapper:    core.NewPathMapper(dir, dir),
	}
}

// prefillMessages adds n synthetic message pairs (user+assistant) to simulate a long history.
// Each message is padded to roughly `charsEach` characters so token estimates are predictable.
func prefillMessages(loop *core.Loop, n, charsEach int) {
	pad := strings.Repeat("x", charsEach)
	for i := 0; i < n; i++ {
		loop.Messages = append(loop.Messages,
			providers.Message{Role: "user", Content: pad},
			providers.Message{Role: "assistant", Content: pad},
		)
	}
}

// ── Threshold tests ───────────────────────────────────────────────────────────

// TestCompressionNotTriggeredBelowThreshold verifies that when the estimated
// token count is below 3/4 of ContextLimit, no extra provider call is made.
func TestCompressionNotTriggeredBelowThreshold(t *testing.T) {
	prov := &capturingProvider{
		responses: []providers.CompleteResponse{
			{AssistantText: "ok"},
		},
	}
	loop := newCompressLoop(t, prov, 80_000)

	// Add a small history — well under threshold.
	prefillMessages(loop, 2, 100)

	if err := loop.Step(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	// Only one provider call expected (the normal completion).
	if len(prov.requests) != 1 {
		t.Errorf("expected 1 provider call, got %d (extra calls = compression fired unexpectedly)", len(prov.requests))
	}
}

// TestCompressionTriggeredAboveThreshold verifies that a large history causes
// a summarization call before the main completion call.
func TestCompressionTriggeredAboveThreshold(t *testing.T) {
	prov := &capturingProvider{
		responses: []providers.CompleteResponse{
			{AssistantText: "summary of earlier work", Usage: providers.Usage{InputTokens: 500, OutputTokens: 50}},
			{AssistantText: "main response"},
		},
	}
	// Very low context limit so the prefilled history exceeds it.
	loop := newCompressLoop(t, prov, 100)

	// 10 pairs × 400 chars each → ~2000 chars → ~500 estimated tokens — well above 75 of 100.
	prefillMessages(loop, 10, 400)

	if err := loop.Step(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}

	// Expect: 1 summarization call + 1 main completion call.
	if len(prov.requests) < 2 {
		t.Fatalf("expected >= 2 provider calls (summary + completion), got %d", len(prov.requests))
	}

	// The first request is the summarization call — it should contain the summarizer system message.
	summaryReq := prov.requests[0]
	if len(summaryReq.Messages) == 0 {
		t.Fatal("summarization request has no messages")
	}
	firstRole := summaryReq.Messages[0].Role
	if firstRole != "system" {
		t.Errorf("summarization request first message role = %q, want system", firstRole)
	}
}

// ── Role correctness ─────────────────────────────────────────────────────────

// TestCompressedSummaryRoleIsSystem verifies that the synthetic summary message
// injected into loop.Messages uses Role="system", not "user".
func TestCompressedSummaryRoleIsSystem(t *testing.T) {
	prov := &capturingProvider{
		responses: []providers.CompleteResponse{
			{AssistantText: "compressed summary text", Usage: providers.Usage{InputTokens: 100, OutputTokens: 20}},
			{AssistantText: "final answer"},
		},
	}
	loop := newCompressLoop(t, prov, 100)
	prefillMessages(loop, 10, 400)

	if err := loop.Step(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	// After Step, loop.Messages should start with the summary message.
	if len(loop.Messages) == 0 {
		t.Fatal("loop.Messages is empty after step")
	}
	first := loop.Messages[0]
	if first.Role != "system" {
		t.Errorf("first message after compression has Role=%q, want system", first.Role)
	}
	if !strings.Contains(first.Content, "CONTEXT SUMMARY") {
		t.Errorf("first message content missing CONTEXT SUMMARY marker: %q", first.Content)
	}
}

// ── Budget accounting ─────────────────────────────────────────────────────────

// TestCompressionBudgetAccounted verifies that tokens consumed by the
// summarization call are charged to the BudgetTracker.
func TestCompressionBudgetAccounted(t *testing.T) {
	const summaryInput = 400
	const summaryOutput = 60

	prov := &capturingProvider{
		responses: []providers.CompleteResponse{
			{
				AssistantText: "summary",
				Usage:         providers.Usage{InputTokens: summaryInput, OutputTokens: summaryOutput, CostUSD: 0.002},
			},
			{
				AssistantText: "done",
				Usage:         providers.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}
	loop := newCompressLoop(t, prov, 100)
	prefillMessages(loop, 10, 400)

	if err := loop.Step(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	used := loop.Budget.Budget()
	if used.UsedTokens < summaryInput+summaryOutput {
		t.Errorf("UsedTokens=%d, want at least %d (summary tokens must be counted)",
			used.UsedTokens, summaryInput+summaryOutput)
	}
	if used.UsedCostUSD < 0.002 {
		t.Errorf("UsedCostUSD=%.4f, want at least 0.0020 (summary cost must be counted)", used.UsedCostUSD)
	}
}

// ── Memory injection ──────────────────────────────────────────────────────────

// TestMemoryInjectedIntoProviderCall verifies that when Policy.MemoryPath points
// to an existing MEMORY.md, its contents appear as a system message in the
// messages sent to the provider.
func TestMemoryInjectedIntoProviderCall(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "MEMORY.md")
	memContent := "- 2026-03-04: decided to use system role for summaries"
	if err := os.WriteFile(memPath, []byte(memContent), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &capturingProvider{
		responses: []providers.CompleteResponse{{AssistantText: "ok"}},
	}

	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	pol := policy.Default()
	pol.MemoryPath = memPath

	loop := &core.Loop{
		Run:       &core.Run{ID: "t", Dir: dir, TraceFile: tracePath},
		Provider:  prov,
		Tools:     tools.NewRegistry(nil),
		Policy:    pol,
		Trace:     trace,
		Budget:    core.NewBudgetTracker(&core.Budget{MaxSteps: 10, MaxTokens: 100_000}),
		ConfirmFn: func(_, _ string) bool { return true },
	}

	if err := loop.Step(context.Background(), "what do you remember?"); err != nil {
		t.Fatal(err)
	}

	if len(prov.requests) == 0 {
		t.Fatal("no provider calls recorded")
	}

	msgs := prov.requests[0].Messages
	found := false
	for _, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, memContent) {
			found = true
			break
		}
	}
	if !found {
		t.Error("memory content not found in any system message sent to provider")
	}
}

// TestMemoryAbsentWhenFileNotFound verifies that when MEMORY.md does not exist,
// no extra system message is injected (no panic, no error).
func TestMemoryAbsentWhenFileNotFound(t *testing.T) {
	prov := &capturingProvider{
		responses: []providers.CompleteResponse{{AssistantText: "ok"}},
	}

	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	pol := policy.Default()
	pol.MemoryPath = filepath.Join(dir, "MEMORY.md") // file does not exist

	loop := &core.Loop{
		Run:       &core.Run{ID: "t", Dir: dir, TraceFile: tracePath},
		Provider:  prov,
		Tools:     tools.NewRegistry(nil),
		Policy:    pol,
		Trace:     trace,
		Budget:    core.NewBudgetTracker(&core.Budget{MaxSteps: 10, MaxTokens: 100_000}),
		ConfirmFn: func(_, _ string) bool { return true },
	}

	if err := loop.Step(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	// Exactly one system message expected (the static system prompt only).
	msgs := prov.requests[0].Messages
	systemCount := 0
	for _, m := range msgs {
		if m.Role == "system" {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected 1 system message (prompt only), got %d", systemCount)
	}
}

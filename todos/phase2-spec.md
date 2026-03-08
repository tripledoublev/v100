# Phase 2: Research Power

Phase 1 delivered: GenParams, Anthropic provider, pluggable scorers, JSONL
datasets, CI, and improved token estimation. 

---

## 2a. Solver Abstractions (COMPLETED)

Implemented pluggable `Solver` interface, `ReactSolver`, `PlanExecuteSolver`, and checkpointing.

---

## 2b. Streaming Provider Interface

**Problem.** `Provider.Complete()` is synchronous — blocks until the full
response arrives. This prevents: real-time token display, time-to-first-token
metrics, mid-generation interrupts, and progressive TUI updates.

**Design.**

```
internal/providers/provider.go  — add StreamComplete to interface
internal/providers/stream.go    — StreamEvent types + helpers
internal/providers/anthropic.go — streaming SSE implementation
internal/providers/openai.go    — streaming SSE implementation
internal/core/loop.go           — prefer streaming when available
```

### Interface

```go
// StreamEvent is a single chunk from a streaming completion.
type StreamEvent struct {
    Type         StreamEventType
    Text         string          // for TokenEvent
    ToolCallID   string          // for ToolCallStart/Delta/End
    ToolCallName string          // for ToolCallStart
    ToolCallArgs string          // for ToolCallDelta (incremental JSON)
    Done         bool            // for DoneEvent
    Usage        Usage           // populated on DoneEvent
    Raw          json.RawMessage // provider-specific chunk
}

type StreamEventType int
const (
    StreamToken StreamEventType = iota
    StreamToolCallStart
    StreamToolCallDelta
    StreamToolCallEnd
    StreamDone
    StreamError
)

// Streamer is optionally implemented by providers that support streaming.
type Streamer interface {
    StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error)
}
```

Use interface assertion (`if s, ok := provider.(Streamer); ok { ... }`) rather
than adding StreamComplete to the base Provider interface. This keeps
non-streaming providers (Codex, Ollama) simple.

### Loop integration

`Loop.Step()` checks if the provider implements `Streamer`. If yes:
1. Call `StreamComplete()` instead of `Complete()`
2. Accumulate text chunks, forward each to `OutputFn` for real-time display
3. Record time-to-first-token in the `model.response` trace event
4. Assemble the final `CompleteResponse` from accumulated chunks
5. Support `ctx.Done()` for mid-generation cancel

Add `TTFT int64` (time-to-first-token in ms) to `ModelRespPayload`.

### Provider implementations

**Anthropic**: POST to `/v1/messages` with `"stream": true`. Parse SSE events:
`message_start`, `content_block_start`, `content_block_delta`,
`content_block_stop`, `message_delta`, `message_stop`. Map to StreamEvent
types.

**OpenAI**: POST to chat completions with `"stream": true`. Parse SSE
`data: {...}` lines. Map `choices[0].delta` to StreamEvent types.

**Gemini, Codex, Ollama**: Skip for Phase 2. They continue using synchronous
`Complete()`. Streaming can be added later.

### TUI integration

The Bubble Tea TUI currently receives complete events via `OutputFn`. With
streaming, it receives partial `StreamToken` events. The TUI model accumulates
these into the response pane, providing character-by-character display.

Add a `StreamingFn func(StreamEvent)` callback to Loop (nil = disabled).

### Test plan

- Unit test: mock streaming provider sends 5 token events + done, loop
  assembles correct CompleteResponse
- Unit test: TTFT is recorded in trace event
- Unit test: context cancellation stops stream consumption
- Integration: Anthropic streaming against live API (manual, not CI)

---

## 2c. Dataset & Experiment Management

**Problem.** `v100 bench` runs prompt×variant grids but has no experiment
grouping, no cross-run statistics, no parameter sweep automation, and no
structured result storage.

**Design.**

```
internal/eval/experiment.go  — experiment lifecycle
internal/eval/results.go     — result aggregation + statistics
cmd/v100/main.go             — experiment subcommands
```

### Experiment model

```go
type Experiment struct {
    ID        string            `json:"id"`
    Name      string            `json:"name"`
    CreatedAt time.Time         `json:"created_at"`
    Config    ExperimentConfig  `json:"config"`
    Status    string            `json:"status"` // "running", "completed", "failed"
    RunIDs    []string          `json:"run_ids"`
}

type ExperimentConfig struct {
    DatasetPath string         `json:"dataset_path,omitempty"`
    BenchPath   string         `json:"bench_path,omitempty"`
    Variants    []BenchVariant `json:"variants"`
    Scorer      string         `json:"scorer"`
    Expected    string         `json:"expected,omitempty"`
    Repeats     int            `json:"repeats"` // run each prompt N times
}
```

Experiments are stored at `runs/experiments/<id>/experiment.json`. Each run
within the experiment gets a normal `runs/<run_id>/` directory with a
`meta.json` that includes `experiment_id`.

### CLI commands

```
v100 experiment create --name "solver-comparison" \
    --bench bench.toml --scorer model_graded --repeats 3

v100 experiment run <experiment_id>

v100 experiment status <experiment_id>
    # Shows: progress, completed/total runs, pass/fail counts

v100 experiment results <experiment_id>
    # Shows: per-variant aggregated stats (mean/std/CI for score, cost,
    # latency, tokens), statistical significance between variants

v100 experiment compare <exp_id_1> <exp_id_2>
    # Cross-experiment comparison
```

### Statistical analysis

`internal/eval/results.go`:

```go
type VariantResults struct {
    Variant     string
    N           int
    Scores      []float64  // 0.0-1.0 from scorer
    PassRate    float64
    MeanCost    float64
    StdCost     float64
    MeanLatency float64
    MeanTokens  float64
    CI95Low     float64    // 95% confidence interval for pass rate
    CI95High    float64
}

// CompareVariants returns a significance test result between two variants.
type SignificanceResult struct {
    Variant1    string
    Variant2    string
    PValue      float64
    Significant bool   // p < 0.05
    Effect      string // "variant1_better", "variant2_better", "no_difference"
}
```

Use Wilson score interval for confidence intervals on pass rate. Use Fisher's
exact test or chi-squared for significance (implement directly — avoid
importing a stats library for two functions).

### Dataset improvements

Extend `LoadDataset` to support CSV (first row = headers, must include
`prompt` column). Detect format by file extension.

Add `v100 dataset validate <path>` command that checks format, counts items,
and reports any issues.

### Test plan

- Unit test: create experiment, add runs, compute results
- Unit test: Wilson CI calculation against known values
- Unit test: significance test with obvious pass/fail split
- Unit test: CSV dataset loading
- Integration: create experiment from bench.toml, run it, view results

---

## 2d. Trace Analysis Engine

**Problem.** `stats.go` computes flat aggregates. There is no behavioral
classification, decision tree reconstruction, cross-run diffing, or failure
root-cause analysis. Raw traces are data; analysis is insight.

**Design.**

```
internal/eval/analysis.go      — behavioral classifiers
internal/eval/diff.go          — cross-run trace diffing
internal/eval/taxonomy.go      — failure root-cause categories
```

### Behavioral classifiers

Analyze a trace and produce labels:

```go
type BehaviorLabel struct {
    Name       string  // "thrashing", "stuck_loop", "backtracking", "efficient", "tool_error_cascade"
    Confidence float64 // 0.0-1.0
    Evidence   string  // human-readable explanation
    EventRange [2]int  // indices into the event slice
}

func ClassifyBehavior(events []core.Event) []BehaviorLabel
```

**Detectors:**

| Label | Heuristic |
|-------|-----------|
| `thrashing` | Same tool called 3+ times with similar args within 5 events |
| `stuck_loop` | Same error message appears 3+ times |
| `backtracking` | Model undoes previous work (writes then rewrites same file, or model text contains "actually" / "let me try again") |
| `efficient` | Direct path: ≤3 tool calls per step, no failures, task completed |
| `tool_error_cascade` | 2+ consecutive tool failures |
| `over_reading` | >5 fs_read calls before any fs_write |
| `context_pressure` | Compression triggered, or token usage >80% of limit |

### Trace diffing

Given two traces, identify where they diverge:

```go
type TraceDiff struct {
    DivergeAtStep int
    DivergeReason string // "different_tool_choice", "different_plan", "error_in_A_not_B"
    StepsOnlyInA  []int
    StepsOnlyInB  []int
    CommonPrefix  int   // number of structurally identical steps
}

func DiffTraces(a, b []core.Event) TraceDiff
```

Compare traces step-by-step: same user input → same tool calls (by name, not
by exact args) → same result status. First divergence is the diff point.

### Failure taxonomy

Classify run failures into categories:

```go
type FailureCategory string
const (
    FailAuth          FailureCategory = "auth_error"
    FailToolError     FailureCategory = "tool_error"
    FailHallucination FailureCategory = "hallucination"
    FailBudget        FailureCategory = "budget_exhaustion"
    FailWrongApproach FailureCategory = "wrong_approach"
    FailNoProgress    FailureCategory = "no_progress"
    FailTimeout       FailureCategory = "timeout"
)

type FailureAnalysis struct {
    Category    FailureCategory
    Confidence  float64
    RootCause   string   // specific error message or event
    Suggestion  string   // remediation hint
}

func AnalyzeFailure(events []core.Event) *FailureAnalysis
```

Heuristics:
- `auth_error`: run.error contains "401", "403", "api key", "auth"
- `tool_error`: >50% tool calls failed
- `budget_exhaustion`: run.end reason is budget_*
- `no_progress`: last 3 steps have identical tool call patterns
- `hallucination`: model references files/functions that don't appear in any
  tool result

### CLI

```
v100 analyze <run_id>
    # Prints: behavior labels, failure analysis (if applicable)

v100 diff <run_id_1> <run_id_2>
    # Prints: divergence point, structural comparison
```

### Export

Add `--format json` flag to analyze/diff/stats commands. JSON output enables
piping into external tools.

### Test plan

- Unit test: each behavior detector against a crafted event sequence
- Unit test: diff two traces that diverge at step 3
- Unit test: failure taxonomy on auth error trace, budget exceeded trace
- Unit test: JSON export format
- Integration: analyze a real run trace, verify labels make sense

---

## 2e. Sandbox Execution

**Problem.** Tools execute against the live filesystem. A misbehaving agent can
`rm -rf /`. For research on untrusted agent behavior and for reproducibility,
isolation is essential.

**Design.**

```
internal/tools/sandbox.go    — workspace snapshot/restore
internal/core/loop.go        — sandbox lifecycle hooks
```

### Workspace snapshots

Before a run starts, snapshot the workspace:

```go
type Sandbox struct {
    WorkspaceDir string
    SnapshotDir  string  // temp dir with copy of workspace
    Active       bool
}

func NewSandbox(workspaceDir string) (*Sandbox, error)  // copies workspace
func (s *Sandbox) Restore() error                        // restores from snapshot
func (s *Sandbox) Commit() error                         // discards snapshot (accept changes)
func (s *Sandbox) Close() error                          // cleanup temp dir
```

**Strategy**: If the workspace is a git repo (common case), use
`git stash --include-untracked` for snapshot and `git stash pop` for restore.
This is fast and handles large repos well. For non-git workspaces, fall back
to `cp -a` into a temp dir.

### Loop integration

```go
type Loop struct {
    // ... existing fields ...
    Sandbox *Sandbox // nil = no sandbox
}
```

If `Sandbox` is non-nil:
- On budget exceeded or run.error, auto-restore workspace
- On normal run.end, prompt user: "Accept workspace changes? [y/n]"
- Trace gets a `sandbox.restore` event when rollback occurs

### CLI

```
v100 run --sandbox "task description"
    # Snapshots workspace before run, restores on failure

v100 run --sandbox --auto-commit "task description"
    # Accepts changes automatically on success
```

### Network restriction (stretch goal)

Not for initial Phase 2 — too platform-specific. Document as Phase 3 item.
For now, the `curl_fetch` tool can be disabled via `tools.enabled` config to
prevent network access.

### Test plan

- Unit test: sandbox snapshot + restore in git repo
- Unit test: sandbox snapshot + restore in non-git dir
- Unit test: sandbox auto-restore on budget exceeded
- Integration: run with --sandbox, make changes, verify restore works

---

## Implementation Order

```
Week 1-2:  2a solver abstractions (react extraction, plan_execute, checkpoint)
Week 3-4:  2d trace analysis (classifiers, failure taxonomy, diff)
Week 5-6:  2c experiment management (experiment model, results, statistics)
Week 7-8:  2b streaming (interface, anthropic impl, loop integration)
Week 9:    2e sandbox (git-based snapshot/restore)
Week 10:   2a reflexion solver (depends on 2c scorer integration)
```

Rationale: solvers first because they change the core loop and everything else
builds on them. Analysis next because it's self-contained and immediately
useful for evaluating solver experiments. Experiments next because they need
analysis. Streaming is independent but lower priority — the synchronous
interface works fine for research. Sandbox last because it's the most
platform-dependent.

## New Event Types

| Event | Payload | Source |
|-------|---------|--------|
| `solver.plan` | `{plan: string, steps: []string}` | plan_execute |
| `solver.replan` | `{reason: string, attempt: int}` | plan_execute |
| `solver.attempt` | `{attempt: int, score: string, value: float64}` | reflexion |
| `solver.reflect` | `{reflection: string}` | reflexion |
| `sandbox.snapshot` | `{method: "git"\|"copy", workspace: string}` | sandbox |
| `sandbox.restore` | `{reason: string}` | sandbox |

## Files to Create/Modify

| File | Action | Size est. |
|------|--------|-----------|
| `internal/core/solver.go` | Create — interface + registry | ~80 lines |
| `internal/core/solver_react.go` | Create — extract from loop.go | ~60 lines |
| `internal/core/solver_plan.go` | Create — plan-then-execute | ~200 lines |
| `internal/core/solver_reflect.go` | Create — reflexion | ~150 lines |
| `internal/core/checkpoint.go` | Create — save/restore | ~40 lines |
| `internal/core/loop.go` | Modify — delegate to solver | ~-30 lines |
| `internal/providers/stream.go` | Create — stream types | ~60 lines |
| `internal/providers/provider.go` | Modify — Streamer interface | ~15 lines |
| `internal/providers/anthropic.go` | Modify — add SSE streaming | ~150 lines |
| `internal/providers/openai.go` | Modify — add SSE streaming | ~120 lines |
| `internal/eval/experiment.go` | Create — experiment lifecycle | ~200 lines |
| `internal/eval/results.go` | Create — aggregation + stats | ~250 lines |
| `internal/eval/analysis.go` | Create — behavior classifiers | ~300 lines |
| `internal/eval/diff.go` | Create — trace diffing | ~120 lines |
| `internal/eval/taxonomy.go` | Create — failure classification | ~150 lines |
| `internal/tools/sandbox.go` | Create — snapshot/restore | ~120 lines |
| `cmd/v100/main.go` | Modify — new commands + flags | ~200 lines |
| `internal/core/types.go` | Modify — new event types | ~40 lines |
| `internal/core/bench.go` | Modify — solver field on variant | ~10 lines |
| `internal/config/config.go` | Modify — solver config | ~20 lines |

**Estimated total**: ~2,300 new/modified lines + tests (~1,200 lines).

## Acceptance Criteria

- [ ] `v100 run --solver plan_execute "task"` generates a plan, executes it, re-plans on failure
- [ ] `v100 run --solver react "task"` behaves identically to current behavior
- [ ] Streaming works for Anthropic provider with real-time TUI display
- [ ] `v100 experiment create/run/results` produces aggregated stats with CIs
- [ ] `v100 analyze <run>` outputs behavior labels and failure categories
- [ ] `v100 diff <run1> <run2>` shows divergence point
- [ ] `v100 run --sandbox` snapshots and restores workspace
- [ ] All existing tests continue to pass
- [ ] New features have >60% test coverage

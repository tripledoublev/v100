# v100 1.0 Readiness Audit and Roadmap

This is a working roadmap for moving `v100` from the current v0.2.x line toward
a 1.0 release. It is intentionally scoped to the agent runtime as it exists
today: a Go CLI/TUI engine for traceable, policy-bound autonomous coding runs.

## Current Readiness

Working score: **48/100**.

The core runtime is real and usable, but 1.0 should not mean "feature-complete."
It should mean the engine is reliable enough for unattended agent work, explicit
about its safety boundaries, observable when it fails, and documented well enough
for a new technical user to run a first task without repo archaeology.

Evidence from the current checkout:

- `go test -coverprofile=/tmp/v100-coverage.out ./cmd/... ./internal/...`
  passes outside the sandbox.
- Total coverage is 46.5%.
- Package coverage highlights: `internal/core` 66.8%,
  `internal/core/executor` 35.9%, `internal/eval` 70.6%,
  `internal/providers` 25.6%, `internal/tools` 60.9%,
  `internal/ui` 43.8%.
- `go run ./cmd/v100 --help` exposes 44 top-level commands.
- `go run ./cmd/v100 tools` lists 48 registered tools.

The sandboxed test run is not sufficient for this audit because this environment
blocks local listener creation for `httptest` and makes some home-directory
fixtures read-only.

## 1.0 Definition

v100 1.0 is done when these conditions are true and verified:

1. **Execution reliability:** the loop, provider calls, message history,
   sandbox snapshots, restore paths, and tool execution have enough integration
   coverage to catch silent failures. Target: at least 70% total statement
   coverage, with focused coverage on cross-package runtime paths.
2. **Production safety:** dangerous tools such as shell, git mutation, ATProto
   writes, and external fetch/index flows have rate limits, dry-run or preview
   modes where appropriate, allowlists or explicit operator gates, and
   circuit-breakers for repeated failures.
3. **Observability and discoverability:** `v100 run --help`, `v100 doctor`, and
   the README make the first successful run obvious. The TUI has a searchable
   help or command palette surface. ACP mode is documented and tested.
4. **Evaluation rigor:** model-graded and reflective scorers have contract
   validation, adversarial input tests, and enough coverage to trust failure
   classifications and metrics derivation.
5. **Autonomous loop robustness:** wake and issue-worker flows record structured
   success/failure signals, detect repeated stagnation, recover from crashes, and
   default away from direct unreviewed changes on protected branches.

## Product Positioning

v100 is a research-grade agent runtime for autonomous coding, optimized for
transparency and control. It is not primarily a chatbot. It is a durable,
observable, sandbox-aware execution engine for agents reasoning over code,
debugging failures, and improving behavior under budget and policy constraints.

The target user is a researcher, power user, or small technical team that values
auditability over convenience. The differentiators are trace replay, explicit
tool schemas, policy gates, sandbox/network controls, durable run artifacts,
benchmarks, evaluation hooks, and long-running wake/research loops.

## Major Work Themes

### 1. Execution Reliability and Safety

Make tool execution and workspace mutation hard to misuse:

- Harden subprocess lifecycle handling in `internal/core/executor`.
- Add regression tests for signal handling, process cleanup, pipe draining, and
  timeout behavior.
- Add rate limits and circuit-breakers around ATProto and news/external fetch
  paths.
- Add dry-run or preview paths for mutation tools where the tool can describe
  the intended side effect before executing it.
- Make pre-push or pre-commit review an explicit unattended-mode safety gate.

### 2. Evaluation Rigor

Make evaluation output defensible:

- Validate scorer contracts before runs start.
- Add targeted tests for model-graded and reflective scorers, including malformed
  model output and prompt-injection-shaped rubric text.
- Document how derived metrics are calculated and where incomplete trace data can
  bias them.
- Validate benchmark expectations when bootstrapping new cases so benchmark data
  does not silently rot after provider or tool changes.

### 3. Observability and Debuggability

Make long-running autonomy legible while it runs and after it fails:

- Add structured wake daemon events for cycle start, goal selection, provider,
  model, result, failure reason, and retry/backoff state.
- Track success/failure rates and repeated-failure clusters for generated goals.
- Add stagnation detection: repeated equivalent goal plus repeated equivalent
  failure should emit an alert and stop burning budget.
- Extend `v100 doctor` to validate behavior directories, TOML shape, tool
  references, config consistency, and common local dependency gaps.

### 4. UX and Discoverability

Reduce the cost of a first successful run:

- Keep the README path linear: install, config, doctor, first run, inspect trace,
  then advanced solvers.
- Add command-level examples for the core operator surfaces: `run`, `resume`,
  `replay`, `tools`, `doctor`, `bench`, `eval`, `wake`, and `research`.
- Add a TUI help or command palette surface for keyboard shortcuts, tool status,
  run state, and available actions.
- Document ACP mode with a minimal client guide and integration test fixture.

### 5. Autonomy Maturity

Make unattended work conservative by default:

- Deduplicate and rank generated wake goals before execution.
- Persist bounded feedback for completed and failed goals.
- Avoid direct commits to protected branches during unattended modes unless the
  operator opts in.
- Record provider/model metadata per run and surface drift-sensitive changes.
- Make impossible or repeatedly failing goals stop cleanly with evidence.

## Milestones

### v0.3.0: Safety and Reliability Foundation

Expected duration: 6 to 8 weeks.

Required work:

- Tool safety: rate limits, pagination limits, circuit-breakers, and explicit
  preview/dry-run paths for high-risk mutations.
- Executor hardening: process cleanup, signal handling, pipe draining, timeout,
  and resource-leak tests.
- Snapshot I/O: investigate incremental or delta snapshots; document the current
  full-copy cost and add load tests for large workspaces.
- Config validation: extend `v100 doctor` to validate behavior dirs, TOML syntax,
  unused or misspelled sections, and tool references.
- ACP stabilization: lifecycle methods, RPC error-code behavior, and integration
  tests.
- Credential posture: document env-var-first auth and add optional keyring or
  external secret-store support where practical.
- Memory/vector store: add TTL or bounded retention, collision-resistant IDs,
  and size-aware eviction.

Release gate:

- Focused tests cover executor lifecycle edge cases and high-risk tool policy.
- `make test` or the equivalent unsandboxed Go suite passes.
- `v100 doctor` catches at least the common config and behavior-dir failures.

### v1.0-rc: Eval, Observability, and UX

Expected duration: 4 to 6 weeks.

Required work:

- Eval coverage and contract validation for model-graded and reflective scorers.
- Benchmark expectation validation and stale-case feedback.
- Structured wake observability: JSON events, success/failure metrics, goal
  decay, model/provider metadata, and repeated-failure alerts.
- Wake safety: goal deduplication, conservative branch behavior, pre-push review,
  and stagnation handling.
- CLI/TUI usability: TUI help palette, first-run docs, examples for core
  commands, and clearer command help.
- Docs: ACP client guide, troubleshooting, security model, and operator workflow
  guide.
- Message-history safety: transaction-like compression semantics and rollback or
  recovery behavior for failed compression passes.

Release gate:

- Overall coverage is at least 70%.
- The wake daemon emits enough structured data to debug a failed unattended run.
- A new user can complete first run -> replay -> inspect tools using documented
  commands only.

### v1.0.0: GA Stabilization

Expected duration: 2 to 3 weeks after the release candidate.

Required work:

- Stress tests for long runs, high token use, large workspaces, provider
  fallback, snapshot load, and repeated tool failures.
- Final documentation pass: user guide, API/trace docs, troubleshooting,
  security model, and architecture notes.
- Release notes and migration guide from v0.2.x.
- Final bug fixes discovered during release-candidate dogfooding.

Release gate:

- No known silent-failure class remains untested or undocumented.
- 1.0 docs describe the actual safety model, not an aspirational one.
- A tagged release can be built and verified through the release workflow.

## Descope Past 1.0

These are useful but should not block 1.0:

- Multi-workspace coordination and cross-project memory sharing.
- Predictive budget strategies and per-goal cost modeling.
- Fine-tuning pipeline automation.
- Custom solver DSLs.
- Music-player or radio integration.
- GEO/SEO-specific tooling.
- Remote compute integrations beyond the current research-loop requirements.

## Top Risks

### Executor subprocess leaks

Impact: long-running agents can leak processes or file descriptors, corrupt tool
output, or fail to cleanly terminate timed-out commands.

Mitigation: add lifecycle tests around process groups, timeouts, signal handling,
stdout/stderr draining, and repeated command execution under load.

### Model drift in autonomous loops

Impact: wake or issue-worker loops can repeat the same bad strategy, spend budget
on impossible goals, or misreport progress.

Mitigation: record provider/model metadata, compare repeated goal/failure
patterns, add stagnation alerts, and stop repeated failures after a bounded
number of attempts.

### External API budget or spam risk

Impact: ATProto, news, and indexing tools can fetch too much, hit rate limits, or
publish/amplify content without enough operator control.

Mitigation: add per-tool quotas, pagination caps, backoff on 429s, dry-run
previews for writes, and explicit allowlists for recurring unattended fetches or
posts.

### Evaluation false confidence

Impact: green benchmarks or model-graded scores can mask brittle parsing,
incomplete trace data, or injected evaluator instructions.

Mitigation: validate scorer output schemas, test malformed and adversarial model
responses, and make metrics assumptions explicit.

## Critical Path

1. Executor hardening.
2. Dangerous-tool guardrails.
3. Config and doctor validation.
4. Message-history safety.
5. Wake observability and stagnation handling.
6. Eval contract hardening.
7. TUI and docs discoverability.
8. Release-candidate dogfooding and stress tests.

At single-maintainer pace, this is roughly 12 to 17 focused weeks of engineering
after scope is kept tight. The main risk to that estimate is broadening 1.0 into
new product surface instead of hardening the runtime that already exists.

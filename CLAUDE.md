# CLAUDE.md — v100

Project-level instructions for Claude Code working in this repo. For general
contribution conventions (build/test/lint, style, structure) see `AGENTS.md`.
For the road to 1.0 see `ROADMAP.md`.

## What v100 is

A Go CLI/TUI agent runtime for traceable, policy-bound autonomous coding runs.
Multi-provider routing, pluggable solvers, schema-bound tools with a Safe/Dangerous
model, sandboxed workspaces, durable trace/replay, eval/bench, and autonomous
wake/research loops. Current line: v0.2.x. Positioning: a research-grade,
auditable execution engine — not a chatbot.

## Current focus: v0.3 — Safety & Reliability

Milestone `v0.3 — Safety & Reliability` groups the active work (issues #218–#224).
Pick the highest-priority open issue in that milestone and close it with a focused
change. Priorities are encoded as labels (`P0: Critical` > `P1: High` > `P2: Strategic`).

| # | Issue | Labels |
|---|-------|--------|
| #218 | Tools Safety — rate-limit + circuit-breaker for ATProto/news | P0, Infrastructure |
| #219 | Executor Hardening — resource leaks, signal handling | P0, Infrastructure |
| #220 | Snapshot I/O — delta snapshots for large repos | P1, Infrastructure |
| #222 | ACP Completion — lifecycle methods, RPC error codes | P1, Infrastructure |
| #223 | Secrets Management — OAuth credential hardening | P1, Infrastructure |
| #224 | Memory Vector Store — TTL, dedup, eviction | P1, Technical Debt |
| #221 | Config Validation — extend `v100 doctor` | P2, Technical Debt |

Each issue carries its own Definition of Done and acceptance criteria.

## Definition of 1.0 (the bar these milestones build toward)

1. **Execution reliability** — ≥70% total coverage, focused on cross-package runtime paths; no silent failures in loop/providers/tools/snapshot/restore.
2. **Production safety** — dangerous tools (shell, git mutation, ATProto writes, external fetch/index) have rate limits, preview/dry-run where sensible, operator gates, and circuit-breakers.
3. **Observability & discoverability** — `v100 run --help`, `v100 doctor`, and the README make a first successful run obvious; TUI has searchable help; ACP is documented and tested.
4. **Evaluation rigor** — model-graded/reflective scorers have contract validation, adversarial-input tests, and trustworthy metrics derivation.
5. **Autonomous loop robustness** — wake/issue-worker record structured success/failure signals, detect stagnation, recover from crashes, and default away from unreviewed changes on protected branches.

Coverage today (outside sandbox): total **46.5%**; v0.3 target **55%+**.

## Descoped — do NOT build before 1.0

Multi-workspace coordination · advanced/predictive budget strategies ·
fine-tuning/dataset-export pipeline · custom solver DSL · music-player TUI ·
GEO/SEO tooling · Sprites/Modal remote-compute integration. These are v1.1+.
Keep PRs scoped to the active issue; no pre-emptive refactors.

## How the autonomous issue-worker actually works

`v100 wake` is **config-driven**, not flag-driven. Flags are only
`--interval`, `--provider`, `--state-path`, `--token`. Mode/repo/budgets come from
the `[wake]` section of `config.toml`:

```toml
[wake]
mode = "issue_worker"        # goal_generator | issue_worker | synthesis
repo = "tripledoublev/v100"  # owner/name for gh issue ops
objective = "Pick the highest-priority open issue in the v0.3 milestone, implement it, verify, commit. Stay within the issue's scope."
issue_limit = 20             # open issues fetched per cycle
provider = ""                # empty = inherit defaults.provider
interval_seconds = 3600
max_failures = 5             # 0 = unlimited; >0 stops a wedged daemon
budget_steps = 40
budget_tokens = 120000
budget_cost_usd = 0.0
```

Per cycle the daemon (`cmd/v100/cmd_wake.go`):
1. Lists open issues (`gh issue list --state open --limit <issue_limit>`), **no label filter** — it takes the first; labels are injected into the agent prompt for prioritization.
2. Runs the agent as `v100 run --auto --unsafe --exit --sandbox --disable-watchdogs` with the wake budgets.
3. The agent makes the change and **must pass** `./scripts/lint.sh`, `go test ./...`, `go build ./...`, then **commits** — it does **not** push or close the issue.
4. The **daemon** verifies the new commit and **pushes directly to the default branch**, then closes the issue. It only pushes/closes when the current branch is the origin default branch (`cmd_wake.go:954`).

**There is no PR step and no built-in human-approval gate.** The real guardrails are: the sandbox + `[sandbox] apply_back`, the lint/test/build verification gate before commit, and the `budget_*` / `max_failures` limits. To keep a human in the loop, either (a) don't run the daemon — drive issues manually and open PRs yourself, or (b) set `wake.objective` to "open a pull request instead of pushing to the default branch" and run from a non-default branch so the auto-push guard blocks direct pushes.

## Conventions (see AGENTS.md for the full version)

- Build: `make build` · Test: `make test` (`go test -race -coverprofile=coverage.out ./cmd/... ./internal/...`) · Lint: `make lint`.
- `gofmt` before submitting; keep package boundaries; mirror tests as `*_test.go`.
- Commits: short imperative, `feat:`/`fix:` prefixes, narrowly scoped, name the subsystem.
- Never commit secrets, traces, caches, or local binaries. Treat sandbox/network-tier/dangerous-tool changes as high-risk and cover them with tests.

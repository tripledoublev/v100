# Repository Guidelines

## Project Structure & Module Organization

This is a Go-based agent runtime and CLI. Cobra commands live in `cmd/v100/`. Runtime logic is under `internal/core/`, tools under `internal/tools/`, providers under `internal/providers/`, terminal UI under `internal/ui/`, evaluation under `internal/eval/`, and memory support under `internal/memory/`. Documentation is in `docs/`, schemas in `schemas/`, assets in `assets/`, and Docker support in `docker/`. Benchmark and dogfood configs use TOML files in `benchmarks/`, `tests/benchmarks/`, and `dogfood/`. Python training-loop experiments are isolated under `research/train-loop/`.

## Build, Test, and Development Commands

- `make build`: builds `./v100` from `./cmd/v100` without running install hooks.
- `./scripts/build.sh`: builds `./v100` and updates the local shell link unless `V100_SKIP_INSTALL=1` is set.
- `go build -o v100 ./cmd/v100`: builds only the CLI binary.
- `make lint` or `./scripts/lint.sh`: runs `golangci-lint` at the version pinned in `.golangci-version`.
- `make test` or `./scripts/test.sh`: runs `go test -race -coverprofile=coverage.out` for `./cmd/...` and `./internal/...`.
- `./v100 doctor`: checks local runtime configuration after building.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt`/`go test` before submitting changes. Keep package names short, lowercase, and aligned with directory purpose. Go files use names such as `cmd_run.go`, `solver_plan.go`, or `workspace_applyback.go`; tests mirror the subject with `_test.go`. Prefer existing package boundaries and helpers over broad new abstractions. Keep generated artifacts, run outputs, caches, and local binaries out of commits unless explicitly intended.

## Testing Guidelines

Place Go tests beside the package they cover using `*_test.go`. Use focused table tests for command flags, solver behavior, tool handling, and policy edge cases. Run `make test` for normal validation; use targeted runs such as `go test ./internal/core -run TestName` while iterating. Keep benchmark scenarios in TOML files under `benchmarks/`, `tests/benchmarks/`, or `dogfood/`.

## Workspace Snapshots

Workspace snapshots default to delta mode. Each snapshot stores a manifest plus content-addressed blobs under the run's `snapshots/content/` directory; unchanged files reuse prior digests and identical file contents are stored once. Full-copy mode remains available through `WorkspaceSnapshotOptions{Mode: SnapshotModeFullCopy}` for comparison and compatibility testing.

Delta restore rebuilds the workspace from the manifest and blob store, so tests should verify restored workspace state instead of assuming every snapshot directory is a full materialized tree. `CaptureAsync` is available for non-preimage backups where callers can wait on the returned `Done` channel before restore; normal pre-mutation snapshots remain synchronous so the captured state is stable before a mutating tool runs.

## Commit & Pull Request Guidelines

Recent history uses short imperative summaries, often with `feat:` or `fix:` prefixes, for example `fix: enable tool execution in synthesis tasks`. Keep commits narrowly scoped and mention affected subsystems when useful. Pull requests should include a concise description, the commands run, linked issues when applicable, and screenshots or terminal output for TUI-facing changes. Call out configuration, provider, network, or sandbox behavior changes explicitly.

## Security & Configuration Tips

Do not commit secrets from `.env`, provider credentials, traces, or local run artifacts. Treat sandbox, network-tier, and dangerous-tool policy changes as high risk and cover them with tests.

## Vector Store Lifecycle

Workspace blackboard vectors live in `blackboard.vectors.json`. New memory
items receive a default 7-day TTL unless an explicit `expires_at` is provided.
The vector store prunes expired entries on load, search, item listing, and
background compaction started for agent steps. It also rejects duplicate embeddings, evicts oldest
items beyond the per-scope cap, and trims oldest records when the JSON store
exceeds the configured size cap. Use `VectorStore.WithOptions` in tests or
specialized stores when a different TTL, per-scope item cap, or store-size cap
is required.

## Roadmap & Current Priorities

`ROADMAP.md` is the working plan toward 1.0. The active phase is the
`v0.3 — Safety & Reliability` milestone (issues #218–#224): tool-safety
rate-limits/circuit-breakers, executor hardening, delta snapshots, ACP
completion, secrets hardening, memory TTL/eviction, and config validation.

When picking work, prefer the highest-priority open issue in that milestone
(`P0: Critical` > `P1: High` > `P2: Strategic`) and keep the change scoped to
that issue — each has a Definition of Done. Do not start descoped v1.1+ work
(multi-workspace, fine-tuning export, custom solver DSL, music-player TUI,
GEO tooling, Sprites/Modal). 1.0 means reliable, safe, observable, and
documented — not feature-complete; the bar is total test coverage ≥70%
(currently 46.5%, v0.3 target 55%+).

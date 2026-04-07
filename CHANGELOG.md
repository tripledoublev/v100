# Changelog

## v0.2.9 — 2026-04-07

**Multi-Platform Release Flow**

This patch release finishes the cross-platform release pipeline, ships platform-specific install scripts, and removes release-blocking platform dependencies from the build path.

### Release and Packaging

- **Multi-platform artifacts** — Release builds now publish Linux, macOS, and Windows binaries for both `amd64` and `arm64` where applicable.
- **Checksum-verified installers** — The shell and PowerShell installers now download the exact release assets and verify them against `checksums.txt`.
- **Release metadata aligned** — The README now documents the shipped binaries and installer entry points so operators can install without guessing asset names.

### Build Compatibility

- **`fs_outline` portability** — The semantic file outline tool now uses the Go AST on non-Windows platforms, removing the tree-sitter dependency from release builds.
- **Windows CLI stubs** — Windows-specific wake and UI stubs keep the command surface and package builds consistent across targets.

## v0.2.8 — 2026-04-04

**Structured News, Persistent Memory, and Interactive Diffing**

This patch release adds a source-aware news retrieval tool, introduces categorized persistent memory with expiry, and ships a side-by-side trace diff TUI, while tightening watchdog discipline, trace analytics, and interactive budget behavior.

### Retrieval and Tooling

- **`news_fetch` tool** — Added a dedicated structured news retrieval tool with feed-first collection, source-aware extraction, normalized headline items, and explicit partial-failure reporting for blocked or thin outlets.
- **Image-aware Codex runs** — Codex provider flows now support image attachments, and policy defaults steer the agent toward direct image inspection when visual evidence is available.
- **Shared blackboard state** — Blackboard memory flows are more useful across runs, with category-aware storage and better review/search behavior.

### Memory and Autonomy

- **Categorized persistent memory** — Durable memory now supports `fact`, `preference`, `constraint`, and `note` categories, plus note expiry/TTL and category-aware retrieval.
- **Memory CLI and review upgrades** — `v100 memory` gained better remember/list/review ergonomics, and expired notes are pruned consistently from retrieval and operator views.
- **Wake goal scanning** — Autonomous wake flows now mine TODOs, dirty files, recent failed runs, and failure artifacts to propose grounded next goals instead of relying on shallow workspace inspection.

### Diffing and TUI

- **Synchronized trace diff model** — Added an alignment-aware sync diff that can realign after mid-trace insertions or deletions, enabling reliable side-by-side comparison.
- **Interactive `v100 diff --tui`** — New Bubble Tea diff viewer renders synchronized transcript panes, keeps scrolling aligned, and jumps directly to the first divergence.
- **Panelized TUI layout** — Extracted panel rendering contracts and tightened pane sizing behavior, fixing status/trace allocation regressions and improving small-terminal behavior.

### Reliability and Analysis

- **Post-tool policy hooks** — Threshold and deduplication hooks now trigger on actual tool results, preventing tool-free turns from consuming failure budget and making repeated tool misuse visible at the right time.
- **Trace analytics accuracy** — Stats and metrics now count executed tools from `tool.result`, classify tool-budget exhaustion more clearly, and avoid double-counting streamed tool-call placeholders.
- **Budget continuation hardening** — Interactive budget continuation and compression telemetry are more explicit, with better handling when runs approach or exhaust token budgets.

## v0.2.7 — 2026-03-22

**Autonomous Wake Hardening and Transcript Fixes**

This patch release hardens the new wake issue-worker loop, restores missing user-message visibility in the UI, and tightens router escalation when cheap-tier models hallucinate tools.

### Wake and Autonomy

- **Wake issue-worker git safety** — Autonomous issue-worker cycles now require a clean working tree before starting, require exactly one new commit, and only auto-push/close from the default branch.
- **Wake sandbox fingerprint baseline** — Sandboxed runs now persist the source-workspace fingerprint at run start, improving apply-back conflict detection and baseline tracking.
- **Issue-worker watchdog handling** — Headless wake issue-worker runs disable read-heavy watchdog interventions that were prematurely stopping autonomous inspection loops.

### UI and Transcript Fixes

- **CLI and TUI user messages restored** — Submitted user messages now appear again in both the CLI transcript and TUI transcript instead of disappearing after the duplicate-echo workaround.
- **CLI prompt echo cleanup** — The terminal prompt line is cleared before event rendering so submitted messages are shown exactly once.
- **Compact failure digest improvements** — Failure digests are auto-printed at the end of failed runs with cleaner operator-facing summaries.

### Routing and Sandbox Behavior

- **Router cheap-tier escalation hardened** — The router now escalates to the smart tier when the cheap model emits unknown or disabled tool names, while still allowing trivial safe mutations like `fs_mkdir` to stay cheap.
- **Sandbox apply-back on `prompt_exit`** — Non-interactive `--exit` runs now allow normal sandbox apply-back, matching the intended successful one-shot flow.

### Reliability and Provider Fixes

- **MiniMax unresolved tool-call sanitization** — Live and provider-facing history now quarantine unresolved tool calls more aggressively to avoid MiniMax request failures.
- **Host network policy regression fixed** — Host-mode sessions no longer bypass `network_tier=off` through the shell tool.
- **Gemini embedding auth corrected** — Gemini embeddings now use real API-key auth instead of the wrong subscription-token path.

## v0.2.6 — 2026-03-21

**MiniMax Default Upgrade and Docs Refresh**

This patch release updates the built-in MiniMax default model to `MiniMax-M2.7` and refreshes stale operator docs so the README and memory notes match current runtime behavior.

### Provider Defaults

- **MiniMax default model upgraded** — Built-in config defaults, provider defaults, tests, and benchmark fixtures now use `MiniMax-M2.7`.
- **Provider docs aligned** — README examples and provider matrix now reflect MiniMax as the built-in default provider and `MiniMax-M2.7` as the default model.

### Documentation

- **README cleanup** — Corrected the default provider guidance, solver count, Go version requirement, and tool-surface description.
- **Compression notes refreshed** — Updated `MEMORY.md` to reflect the current two-pass compression flow with targeted compression before oldest-half fallback.

## v0.2.5 — 2026-03-14

**Harness Cleanup and Watchdog Hardening**

This patch release tightens CLI ergonomics, hardens watchdog and tool-surface behavior, and reduces sandbox artifact noise ahead of the next push.

### UX Improvements

- **CLI dangerous-tool confirmation no longer breaks interactive input** — The Escape listener now backs off while confirmation prompts are active, preventing raw-mode input races during approval flows.
- **CLI transcript readability cleanup** — The transcript now uses plainer labels (`me`, `agent`, `tool`), separates spinner output from assistant text cleanly, and reduces decorative glyph noise.
- **Styled `digest` output** — `v100 digest` now renders a clearer operator-facing failure digest in the CLI while preserving JSON output for machine use.

### Reliability

- **Tool-surface validation is enforced across commands** — Enabled tools are now validated against the registered runtime surface in `run`, `resume`, `eval`/`bench`, and `tools`, with clearer reporting for invalid enabled entries.
- **Registry surface validation** — Enabled tools must now have non-empty descriptions and non-null input schemas, reducing prompt/runtime drift and malformed tool surfaces.
- **Watchdog stop-tools behavior now matches policy** — Inspection/read-heavy watchdogs now force a true final no-tools synthesis turn instead of silently allowing more tool use or terminating early.
- **System interventions no longer masquerade as user input** — Solver steering and watchdog messages are recorded as system messages, improving trace correctness and downstream analysis.
- **Stats/digest tool-call dedupe is step-scoped** — Tool calls are no longer undercounted when call IDs repeat across different steps.

### TUI and Layout

- **Core-size TUI snapshots** — Added snapshot-style regression coverage for narrow, standard, and wide TUI layouts.
- **TUI step interruption support** — Active TUI steps can now be interrupted cleanly without leaving the run in a confused state.

### Sandbox and Artifact Hygiene

- **Apply-back skips more runtime byproducts** — Sandbox apply-back now ignores more harness/runtime and package-manager noise, including `exports/`, `.gocache/`, `.gomodcache/`, `.npm/`, and `node_modules/`.

## v0.2.4 — 2026-03-12

**UX Research Round 2: Dogfooding Fixes**

This release addresses 12 issues found during intensive dogfooding with Gemini and MiniMax providers across ~25 runs.

### Bug Fixes

- **Spinner no longer pollutes non-TTY output** — Spinner frames (`\r\033[K`) are skipped entirely when stdout is redirected to a file or pipe, fixing garbled log captures.
- **Spinner no longer interleaves with tool output** — The model-call spinner is now stopped before rendering tool results, eliminating visual artifacts in live terminal output.
- **`resume --auto` works** — Added missing `--unsafe` and `--yolo` flags to the `resume` command, making `resume --auto --unsafe` and `resume --yolo` functional.
- **`resume` no longer dumps usage on safety errors** — Added `SilenceUsage: true` to the resume command for clean error messages.
- **MiniMax context overflow** — Error code 2013 with "context window exceeds limit" now shows a clear message instead of the misleading "message ordering bug" label.
- **Gemini 429 shows human-readable message** — Rate-limit errors now extract the `message` field from the JSON response (e.g., "You have exhausted your capacity on this model") instead of dumping raw JSON.
- **Stats no longer show zeros for aborted runs** — `ComputeStats` now infers `TotalSteps=1` when no `step.summary` events were emitted but model calls occurred (e.g., budget-exceeded or error-aborted runs).

### UX Improvements

- **Doctor warns instead of failing on unused providers** — Only the default provider causes a failure; other configured-but-unauthenticated providers show warnings (`⚠`) instead of failures (`✗`).
- **`runs` list hides sub-runs by default** — Plan-execute sub-runs are filtered out unless `--all` is passed. Sub-runs display with `↳` prefix when shown.
- **`runs` list filtering** — New flags: `--provider <name>`, `--failed` (show only failed/errored runs), `--all` (include sub-runs).

### Architecture

- **Schema-aware plan_execute planner** — The planning phase now receives tool specifications so the planner knows which tools exist and their parameter schemas, reducing hallucinated tool names.
- **Pre-step budget check** — ReactSolver now checks remaining token budget before entering a step. If remaining tokens are below 5% of total budget, the run exits early with a clear error instead of failing mid-step.
- **`ParentRunID` in run metadata** — `RunMeta` now tracks parent-child relationships between runs for sub-run hierarchy.

## v0.2.3 — 2026-03-10

**Phase 300: Autonomous Optimization Foundation**

This release introduces meta-cognitive tools for agent self-refinement, hardens the TUI layout engine, and enables streaming by default.

### Autonomous Optimization

- **`reflect` tool** — Meta-cognitive self-critique: agents can pause to evaluate progress, plan correctness, and goal alignment. Returns a PASS/FAIL/PARTIAL verdict with reasoning and suggested pivot.
- **`v100 mutate` command** — Trace-driven prompt optimizer that analyzes both qualitative behavioral labels and quantitative failure signatures (step counts, tool error rates, context saturation) to suggest improved prompts.
- **`v100 digest` command** — Compact failure digest for completed runs, surfacing key failure points without the full trace.

### Evaluation & Automation

- **JSON Output** — Added `--format json` to `stats`, `metrics`, `analyze`, `digest`, and `diff` commands for seamless integration with automation pipelines.
- **Scoring Persistence** — Benchmarks and experiments now save full LLM-graded reasoning to `meta.json` and a detailed `evaluation.json` artifact in the run directory.

### Core

- **Streaming by Default** — Token streaming is now enabled by default for all providers that support it.
- **Compression Telemetry** — Enhanced context compression events with token tracking for Anthropic and MiniMax providers.

### UI & UX

- **Dynamic TUI Layout** — Proportional height allocation ensures perfect column alignment across all terminal sizes and pane combinations.
- **Overflow-Safe Status Pane** — Status pane text wrapping no longer breaks right-column height; trace pane absorbs the difference.

## v0.2.2 — 2026-03-10

**Phase 250: Harness Stabilization & Mission Control**

This release focuses on operator experience, TUI aesthetics, and provider hardening to support long-horizon research.

### UIs

- **Mission Control TUI** — Re-architected the right column to include three persistent panes: Trace, Visual Inspector, and Status.
- **Visual Inspector** — New gaming-inspired dashboard with real-time entropy gauges for token window saturation, step budget, and reasoning intensity (I/O ratio).
- **Cognitive Heartbeat** — Animated ASCII pulse indicating real-time agent cognitive activity.
- **Radio Station Selector** — Dedicated modal (`Alt+R` or `/radio`) for selecting ambient background stations by name. Renamed "Radiojar" to "Radio Al Hara".
- **Typing Hygiene** — Removed conflicting single-key radio shortcuts (`n`, `p`, `1`) to prevent interference with text input.
- **Layout Math** — Refined vertical budgeting to ensure all panes fit perfectly across different terminal sizes.

### Core & CLI

- **Non-Interactive Mode** — New `--exit` flag for `v100 run` that executes the initial prompt and automatically finalizes the run without entering the interactive loop.
- **MiniMax Hardening** — Implemented contiguous tool-result ordering to fix Error 2013.
- **Improved Diagnostics** — Explicit logging for message ordering bugs and Gemini multi-tool desyncs.

### Dogfooding

- **Expanded Quest Pack** — Added DF-12 (Non-Interactive Smoke) and updated DF-07/DF-08 to include MiniMax as a standard benchmark provider.

## v0.2.0 — 2026-03-09

**Phase 100: Recursive Self-Evolution**

This release introduces the first milestone of the self-evolution engine, allowing agents to distill their own trajectories and author new tools at runtime.

### Self-Evolution Engine

- **Distill command** — `v100 distill <run_id>` converts JSONL traces into ShareGPT-formatted datasets for model fine-tuning and DPO.
- **Dynamic Tool Registry** — support for `RegisterAndEnable` at runtime, enabling agents to expand harness capabilities without re-compilation.
- **Automatic Build Feedback** — modified `internal/core/loop.go` to trigger `go build ./...` after every workspace mutation, injecting compiler errors as a `SYSTEM ALERT` to enforce a reality-check loop.

### Dynamic Tools

- **`sql_search`** — Execute SQL queries against local SQLite databases with path sanitization.
- **`graphviz`** — Render DOT graph definitions into images (PNG/SVG) for architectural visualization.

### Improvements

- **Dependency Tracking** — Added `github.com/mattn/go-sqlite3` for local structured data operations.
- **Documentation** — New DF-11 quest in `dogfood/` for verifying self-evolution trajectories.

## v0.0.2 — 2026-03-08

Initial release of v100, an experimental agent harness for studying long-horizon LLM behavior.

### Core

- **Agent loop** — ReAct-style tool-using agent loop with structured JSONL traces
- **Budget enforcement** — hard limits on steps, tokens, and cost (`--budget-steps`, `--budget-tokens`, `--budget-cost`)
- **Context compression** — automatic context window management with compression events
- **Dangerous tool confirmation** — CLI stdin prompt or TUI Ctrl+Y/Ctrl+N

### Providers

- **Codex** — ChatGPT subscription via PKCE OAuth (`v100 login`)
- **OpenAI** — standard API access (`OPENAI_API_KEY`)
- **Gemini** — Google subscription via OAuth (`v100 login --provider gemini`)
- **Ollama** — fully local models, no API key required
- **Anthropic** — Claude API access (`ANTHROPIC_API_KEY` or `v100 login --provider anthropic`)
- **Retry/backoff middleware** — unified retry handling across providers for 429 and 5xx responses
- **Model metadata discovery** — providers expose context windows, pricing hints, and free/paid status to the harness

All providers support tool calling and generation parameters (temperature, top_p, top_k, max_tokens, seed).

### Solvers

- **ReactSolver** — classic ReAct loop (default)
- **PlanExecuteSolver** — two-phase plan-then-execute with automatic replanning on failure (`--solver plan_execute`, `--max-replans`)

### Sandbox

- **Docker executor** — isolated container execution with hardened security (seccomp, dropped capabilities, no-new-privileges, PID limits)
- **Network policy** — configurable network isolation (`off` or `open`)
- **Snapshots** — checkpoint and restore sandbox state during runs
- **Apply-back** — merge sandbox changes back to host workspace (`manual`, `on_success`, `never`)

### Tools (23 built-in)

`fs_read`, `fs_write`, `fs_list`, `fs_mkdir`, `fs_outline`, `sh`, `git_status`, `git_diff`, `git_commit`, `git_push`, `sem_diff`, `sem_impact`, `sem_blame`, `patch_apply`, `project_search`, `curl_fetch`, `agent`, `dispatch`, `orchestrate`, `blackboard_read`, `blackboard_write`, `blackboard_store`, `blackboard_search`

### Multi-Agent

- **Sub-agent delegation** — `agent` tool spawns bounded child loops
- **Named specialists** — config-driven roles via `[agents.<name>]`
- **Orchestration** — `orchestrate` tool supports `fanout` and `pipeline` patterns
- **Shared state** — blackboard tools for cross-agent coordination with vectorized memory
- **Reflection turn** — internal confidence check before dangerous tool execution

### Evaluation

- **Run scoring** — `v100 score <run_id> pass|fail|partial`
- **Run statistics** — `v100 stats`, `v100 metrics`, `v100 compare`
- **Metadata-aware reporting** — `meta.json`, `stats`, `compare`, and `query` surface model context/pricing metadata
- **Batch benchmarks** — `v100 bench <config.toml>` with provider/model/parameter variants
- **Experiments** — `v100 experiment create|run|results` for multi-variant statistical testing
- **Behavioral analysis** — `v100 analyze` with automatic failure classification
- **Trace diffing** — `v100 diff` to find divergence between runs
- **Run querying** — `v100 query --tag key=val --score pass`
- **Pluggable scorers** — exact_match, contains, regex, script, model_graded

### UIs

- **CLI** — line-by-line streaming output (default)
- **TUI** — Bubble Tea 3-pane interface with transcript, trace, and input panes

### Trace

- 21 structured event types covering run lifecycle, model calls, tool execution, solver planning, sandbox snapshots, agent delegation, and context compression
- Deterministic replay with `--replace-model` and `--inject-tool` for counterfactual analysis
- Run metadata with names, tags, and scores for later querying

### Infrastructure

- **`v100 doctor`** — health check for providers, tools, and configuration
- **`v100 dev`** — supervisor that rebuilds on `.v100-reload`
- **`v100 config init`** — generates default config and OAuth credential templates
- **CI** — GitHub Actions with `go test -race`, `go vet`, pinned `golangci-lint`, and hardened semantic tool detection

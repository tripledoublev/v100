# Changelog

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

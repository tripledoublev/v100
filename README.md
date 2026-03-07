# v100

v100 is an experimental agent harness for studying long-horizon LLM behavior.

It provides a modular runtime for tool-using language model agents where every run is treated as an observable experiment. Model calls, tool execution, context compression, and delegation events are emitted as structured traces that can be replayed and analyzed.

The goal is to make agent behavior measurable and reproducible so different prompting strategies, tool policies, and orchestration approaches can be systematically evaluated.

## Architecture

v100 runs a core agent loop that orchestrates model calls, tool execution, and optional sub-agent delegation, while emitting structured events into an append-only trace.

## Features

- **Durable traces** — every run is logged as append-only JSONL (`runs/<id>/trace.jsonl`)
- **Run metadata + scoring** — attach names/tags and score outcomes for later analysis
- **Evaluation tooling** — per-run stats, run comparisons, and batch bench execution
- **Trace-derived diagnostics** — efficiency/behavior metrics and automatic failure classification
- **Delegated sub-agents** — `agent` tool can spawn bounded child loops
- **Named specialist agents** — config-driven roles via `[agents.<name>]` and role dispatching
- **Coordination patterns** — `orchestrate` tool supports `fanout` and `pipeline` execution
- **Shared run state** — blackboard tools provide cross-agent coordination via `runs/<id>/blackboard.md`
- **Dispatch telemetry** — per-role dispatch success/cost/tokens appear in `v100 metrics`
- **Tool execution** — file system, shell, git, patch, curl, ripgrep search
- **Dangerous tool confirmation** — CLI stdin prompt or TUI Ctrl+Y/Ctrl+N
- **Budget enforcement** — hard limits on steps, tokens, and cost
- **Resume & replay** — pick up any run from its trace; pretty-print transcripts
- **Four providers** — ChatGPT subscription (Codex), Gemini subscription, OpenAI API, or local Ollama
- **Two UIs** — line-by-line CLI (default) or Bubble Tea 3-pane TUI (`--tui`)
- **Dev supervisor** — restart on demand by creating `.v100-reload`

## Providers

OAuth client config for the subscription providers lives outside the repo at `~/.config/v100/oauth_credentials.json`:

```json
{
  "codex_client_id": "YOUR_CODEX_CLIENT_ID",
  "gemini_client_id": "YOUR_GEMINI_CLIENT_ID",
  "gemini_client_secret": "YOUR_GEMINI_CLIENT_SECRET"
}
```

### ChatGPT subscription (default)

Uses your existing ChatGPT Plus/Pro plan — no separate API billing. Authenticate directly with `v100 login` after filling `~/.config/v100/oauth_credentials.json`:

```bash
v100 login   # opens browser → completes OAuth → saves token to ~/.config/v100/auth.json
```

Model: `gpt-5.3-codex`

### OpenAI API

Standard pay-as-you-go API access.

```bash
export OPENAI_API_KEY=sk-...
v100 run --provider openai --model gpt-4o
```

### Gemini subscription

Uses your existing Gemini Pro / Google One AI Premium plan — no separate API billing. Authenticate with `v100 login --provider gemini` after filling `~/.config/v100/oauth_credentials.json`:

```bash
v100 login --provider gemini   # opens browser → completes OAuth → saves token
v100 run --provider gemini     # uses gemini-2.5-flash by default
v100 run --provider gemini --model gemini-2.5-pro
v100 run --provider gemini --model gemini-3-pro-preview
```

Models: `gemini-2.5-flash` (default), `gemini-2.5-pro`, `gemini-3-pro-preview`, `gemini-3-flash-preview`

### Ollama (local)

Run fully local models via Ollama (no API key required).

```bash
ollama run qwen3.5:9b
v100 run --provider ollama --model qwen3.5:9b
```

### Provider matrix

| Provider | Auth | Default model | Local state | Best for |
|----------|------|---------------|-------------|----------|
| `codex` | `~/.config/v100/oauth_credentials.json` + `v100 login` | `gpt-5.3-codex` | `~/.config/v100/auth.json` | subscription-backed coding runs |
| `gemini` | `~/.config/v100/oauth_credentials.json` + `v100 login --provider gemini` | `gemini-2.5-flash` | `~/.config/v100/gemini_auth.json` | subscription-backed Gemini comparison runs |
| `openai` | `OPENAI_API_KEY` | `gpt-4o` | none | API-driven experiments |
| `ollama` | local Ollama daemon | `qwen3.5:2b` | none | fully local runs |

### Known limitations

- Provider behavior differs noticeably; the same prompt can produce very different tool-use patterns across Codex, Gemini, OpenAI, and Ollama.
- Subscription providers require a local OAuth client config file before `v100 login` will work.
- Underspecified prompts can still trigger over-eager exploration on some models. Use `--budget-steps`, `--budget-tokens`, and default dangerous-tool confirmation when evaluating a new provider or prompt style.

## Install

```bash
git clone https://github.com/tripledoublev/v100
cd v100
go build -o v100 ./cmd/v100/
```

Requires Go 1.21+. Optional: `rg` (ripgrep) for `project_search`, `patch` for `patch_apply`.

## Quick start

```bash
# Initialize config
v100 config init

# This writes ~/.config/v100/config.toml and, if missing,
# ~/.config/v100/oauth_credentials.json as a blank template

# Fill ~/.config/v100/oauth_credentials.json with your OAuth client values

# Authenticate with ChatGPT subscription
v100 login

# Verify setup
v100 doctor

# Start a run (uses ChatGPT subscription by default)
v100 run

# With a step budget
v100 run --budget-steps 10

# Use OpenAI API instead
v100 run --provider openai

# Use local Ollama instead
v100 run --provider ollama --model qwen3.5:9b

# Enable TUI
v100 run --tui

# Name and tag runs for later querying
v100 run --name "parser refactor" --tag team=core --tag sprint=12
```

## Files created

`v100 config init` and the login flows create a small set of local files:

- `~/.config/v100/config.toml` — main runtime config
- `~/.config/v100/oauth_credentials.json` — local OAuth client config for Codex/Gemini
- `~/.config/v100/auth.json` — Codex subscription token after `v100 login`
- `~/.config/v100/gemini_auth.json` — Gemini subscription token after `v100 login --provider gemini`
- `runs/<run_id>/` — trace, metadata, and artifacts for each run

## Commands

| Command | Description |
|---------|-------------|
| `v100 run` | Start interactive agent run |
| `v100 resume <run_id>` | Continue a run from its trace |
| `v100 replay <run_id>` | Pretty-print run transcript |
| `v100 tools` | List registered tools |
| `v100 providers` | List configured providers |
| `v100 config init` | Write default config to `~/.config/v100/config.toml` |
| `v100 doctor` | Check provider auth, tools, run dir |
| `v100 login` | Authenticate via browser OAuth (ChatGPT Plus/Pro) |
| `v100 logout` | Remove stored OAuth token |
| `v100 score <run_id> <pass|fail|partial> [notes...]` | Score a completed run |
| `v100 stats <run_id>` | Compute stats from trace events |
| `v100 metrics <run_id>` | Compute trace-derived efficiency/behavior metrics and auto-classification |
| `v100 compare <run_id> <run_id> [run_id...]` | Compare multiple runs side-by-side |
| `v100 bench <bench.toml>` | Batch-run prompt/provider/model variants |
| `v100 query [--tag k=v ...] [--score pass|fail|partial]` | Filter runs by metadata |
| `v100 dev` | Rebuild/restart dev binary when `.v100-reload` appears |

### Deterministic replay

```bash
v100 replay --deterministic <run_id>
v100 replay --deterministic --step <run_id>
v100 replay --deterministic --replace-model gpt-5.4 <run_id>
v100 replay --deterministic --inject-tool project_search="parser.go:123" <run_id>
```

In deterministic mode, model responses and tool outputs are replayed from trace records.
`--step` pauses between model/tool events for debugger-style inspection.
`--replace-model` runs recorded `model.call` prompts against a different model and prints a counterfactual response.
`--inject-tool` overrides recorded tool outputs in replayed prompts for what-if experiments.

### `v100 run` flags

```
--provider string       Provider name (codex, gemini, openai, ollama)
--model string          Model override
--budget-steps int      Max steps before halting
--budget-tokens int     Max tokens before halting
--budget-cost float     Max cost in USD before halting
--max-tool-calls-per-step int  Max tool calls per step
--confirm-tools string  always | dangerous | never (default: dangerous)
--auto                  Auto-approve all tool calls
--unsafe                Disable path guardrails
--tui                   Enable Bubble Tea TUI
--tui-no-alt            Disable alternate screen
--tui-plain             Force monochrome rendering
--tui-debug             Write TUI debug log in run directory
--workspace string      Workspace directory for tool operations
--name string           Human-readable run name (meta.json)
--tag key=value         Repeatable run tags (meta.json)
```

Default workspace is the current directory where `v100 run` is launched.

## Tools

| Tool | Danger | Description |
|------|--------|-------------|
| `fs_read` | safe | Read file contents |
| `fs_write` | **dangerous** | Write/append to file |
| `fs_list` | safe | List directory entries |
| `fs_mkdir` | safe | Create directory |
| `sh` | **dangerous** | Execute shell command |
| `git_status` | safe | Git status |
| `git_diff` | safe | Git diff |
| `git_commit` | **dangerous** | Stage and commit |
| `git_push` | **dangerous** | Push current branch |
| `sem_diff` | safe | Semantic entity-level diffing (functions, classes) |
| `sem_impact` | safe | Impact analysis for specific code entities |
| `sem_blame` | safe | Entity-level blame for a file |
| `patch_apply` | **dangerous** | Apply unified diff |
| `project_search` | safe | Ripgrep search |
| `curl_fetch` | safe | Fetch URL content |
| `agent` | **dangerous** | Spawn a bounded sub-agent run |
| `dispatch` | **dangerous** | Dispatch a task to a named agent role from config |
| `orchestrate` | **dangerous** | Coordinate multiple dispatches with fanout/pipeline patterns |
| `blackboard_read` | safe | Read shared run blackboard |
| `blackboard_write` | **dangerous** | Append/overwrite shared run blackboard |

Dangerous tools require confirmation unless `--auto` or `--confirm-tools never` is set.

Recommended defaults:
- keep `confirm_tools = "dangerous"` for normal interactive use
- avoid `--auto` when trying a new provider or prompt pattern
- set step/token budgets when evaluating model behavior

## Config

Default location: `~/.config/v100/config.toml`

```toml
[providers.codex]
type = "codex"
default_model = "gpt-5.3-codex"

[providers.openai]
type = "openai"
default_model = "gpt-4o"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[providers.ollama]
type = "ollama"
default_model = "qwen3.5:9b"
base_url = "http://localhost:11434"

[providers.gemini]
type = "gemini"
default_model = "gemini-2.5-flash"

[agents.researcher]
system_prompt = "You are a researcher agent. Find and read relevant code and return concise findings. Do not modify files."
tools = ["fs_read", "fs_list", "project_search"]
model = ""
budget_steps = 15
budget_tokens = 20000
budget_cost_usd = 0.0

[defaults]
provider = "codex"
confirm_tools = "dangerous"
budget_steps = 50
budget_tokens = 100000
budget_cost_usd = 0.0
tool_timeout_ms = 30000
max_tool_calls_per_step = 50
context_limit = 80000
```

## Run layout

```
runs/<run_id>/
  trace.jsonl     # append-only event log
  meta.json       # run metadata: name/tags/provider/model/score
  blackboard.md   # shared scratchpad for multi-agent coordination
  artifacts/      # per-run artifact files
  tui.debug.log   # optional, if --tui-debug
```

## Evaluation workflow

```bash
# Score a run
v100 score <run_id> pass "completed task without manual fixes"

# Inspect one run
v100 stats <run_id>
v100 metrics <run_id>

# Compare several runs
v100 compare <run_a> <run_b> <run_c>

# Query by metadata
v100 query --tag team=core --score pass
```

### Debugging a run

```bash
# Verify auth, provider setup, and local tools
v100 doctor
v100 providers

# Inspect one run in more detail
v100 stats <run_id>
v100 metrics <run_id>
v100 replay <run_id>
v100 replay --deterministic <run_id>
```

## Multi-agent quick examples

```text
Call dispatch with {"agent":"researcher","task":"Find replay implementation and list key files."}
Call orchestrate with {"pattern":"fanout","tasks":[{"agent":"researcher","task":"Map replay"},{"agent":"reviewer","task":"List risks"}]}
Call blackboard_read with {}
```

## Bench config example

```toml
name = "prompt-rewrite-v1"

[[prompts]]
message = "Refactor parser for streaming mode."

[[variants]]
name = "codex-default"
provider = "codex"
model = "gpt-5.3-codex"
budget_steps = 20
```

Run with:

```bash
v100 bench ./bench.toml
```

## Dev mode

`v100 dev` runs a supervisor that rebuilds/restarts the local binary when
`.v100-reload` exists in the project root.

```bash
v100 dev
touch .v100-reload
```

## TUI keybinds

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Tab` | Cycle focus (input → transcript → trace) |
| `Ctrl+T` | Toggle trace pane |
| `Ctrl+A` | Copy full plain-text transcript |
| `Ctrl+Y` | Approve dangerous tool |
| `Ctrl+N` | Deny dangerous tool |
| `Ctrl+C` | Quit |

## License

MIT

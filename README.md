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
- **Delegated sub-agents** — `agent` tool can spawn bounded child loops
- **Tool execution** — file system, shell, git, patch, curl, ripgrep search
- **Dangerous tool confirmation** — CLI stdin prompt or TUI Ctrl+Y/Ctrl+N
- **Budget enforcement** — hard limits on steps, tokens, and cost
- **Resume & replay** — pick up any run from its trace; pretty-print transcripts
- **Two providers** — ChatGPT subscription (no API billing) or OpenAI API
- **Two UIs** — line-by-line CLI (default) or Bubble Tea 3-pane TUI (`--tui`)
- **Dev supervisor** — restart on demand by creating `.v100-reload`

## Providers

### ChatGPT subscription (default)

Uses your existing ChatGPT Plus/Pro plan — no separate API billing. Authenticate directly with `v100 login` (opens browser for PKCE OAuth):

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

# Enable TUI
v100 run --tui

# Name and tag runs for later querying
v100 run --name "parser refactor" --tag team=core --tag sprint=12
```

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
| `v100 compare <run_id> <run_id> [run_id...]` | Compare multiple runs side-by-side |
| `v100 bench <bench.toml>` | Batch-run prompt/provider/model variants |
| `v100 query [--tag k=v ...] [--score pass|fail|partial]` | Filter runs by metadata |
| `v100 dev` | Rebuild/restart dev binary when `.v100-reload` appears |

### Deterministic replay

```bash
v100 replay --deterministic <run_id>
v100 replay --deterministic --step <run_id>
```

In deterministic mode, model responses and tool outputs are replayed from trace records.
`--step` pauses between model/tool events for debugger-style inspection.

### `v100 run` flags

```
--provider string       Provider name (codex, openai)
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
| `patch_apply` | **dangerous** | Apply unified diff |
| `project_search` | safe | Ripgrep search |
| `curl_fetch` | safe | Fetch URL content |
| `agent` | **dangerous** | Spawn a bounded sub-agent run |

Dangerous tools require confirmation unless `--auto` or `--confirm-tools never` is set.

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
  tui.debug.log   # optional, if --tui-debug
```

## Evaluation workflow

```bash
# Score a run
v100 score <run_id> pass "completed task without manual fixes"

# Inspect one run
v100 stats <run_id>

# Compare several runs
v100 compare <run_a> <run_b> <run_c>

# Query by metadata
v100 query --tag team=core --score pass
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

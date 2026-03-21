# v100

v100 is an experimental agent harness for studying long-horizon LLM behavior.

It provides a modular runtime for tool-using language model agents where every run is treated as an observable experiment. Model calls, tool execution, context compression, and delegation events are emitted as structured traces that can be replayed and analyzed.

The goal is to make agent behavior measurable and reproducible so different prompting strategies, tool policies, and orchestration approaches can be systematically evaluated.

## Features

- **Durable traces** — every run is logged as append-only JSONL (`runs/<id>/trace.jsonl`) with 21 structured event types
- **Two solvers** — ReAct loop (default) and Plan-Execute with automatic replanning
- **Sandbox execution** — Docker-based isolated containers with hardened security, snapshots, and apply-back
- **Run metadata + scoring** — attach names/tags and score outcomes for later analysis
- **Evaluation tooling** — per-run stats, run comparisons, experiments, and batch bench execution
- **Trace-derived diagnostics** — efficiency/behavior metrics and automatic failure classification
- **Phase 300 optimization** — autonomous agent refinement via reflective scoring and prompt mutation
- **Self-evolution engine** — agents can distill trajectories and author new tools at runtime
- **Delegated sub-agents** — `agent` tool can spawn bounded child loops
- **Named specialist agents** — config-driven roles via `[agents.<name>]` and role dispatching
- **Coordination patterns** — `orchestrate` tool supports `fanout` and `pipeline` execution
- **Shared run state** — blackboard tools provide cross-agent coordination via vectorized memory
- **Reflection turn** — agents perform an internal confidence-check before executing dangerous tools
- **Streaming** — real-time token streaming from providers that support it
- **Tool execution** — 26+ built-in tools: file system, shell, git, patch, curl, compact web extraction, ripgrep search, semantic parsing, sql_search, graphviz, reflect, and multi-agent coordination
- **Dangerous tool confirmation** — CLI stdin prompt or TUI Ctrl+Y/Ctrl+N
- **Budget enforcement** — hard limits on steps, tokens, and cost
- **Build verification loop** — automatically runs `go build ./...` after every workspace mutation and injects compiler errors as a diagnostic alert
- **Resume & replay** — pick up any run from its trace; pretty-print transcripts
- **Six providers** — Codex (ChatGPT subscription), Gemini subscription, OpenAI API, Anthropic API, Minimax, or local Ollama
- **Two UIs** — line-by-line CLI (default) or Bubble Tea "Mission Control" TUI (`--tui`)
- **Dev supervisor** — restart on demand by creating `.v100-reload`

## Install

```bash
git clone https://github.com/tripledoublev/v100
cd v100
go build -ldflags "-X main.version=v0.2.5" -o v100 ./cmd/v100/
./v100 install
```

`./v100 install` creates `~/.local/bin/v100` as a symlink to the repo-local `./v100`, so future `go build -o v100 ./cmd/v100/` rebuilds automatically update the shell-resolved `v100`.

Requires Go 1.21+. Optional: `rg` (ripgrep) for `project_search`, `patch` for `patch_apply`, `docker` for sandbox execution, `mpv` or `ffplay` for `radio`.

Pre-built binaries are available on the [releases page](https://github.com/tripledoublev/v100/releases).

## Quick start

```bash
# Initialize config
v100 config init

# Fill ~/.config/v100/oauth_credentials.json with your OAuth client values
# (only needed for Codex/Gemini subscription providers)

# Authenticate with ChatGPT subscription
v100 login

# Verify setup
v100 doctor

# Start a run (uses ChatGPT subscription by default)
v100 run

# Start a non-interactive run (executes prompt then exits)
v100 run --exit "Summarize the project structure"

# With a step budget
v100 run --budget-steps 10

# Use OpenAI API
v100 run --provider openai

# Use Anthropic API
v100 run --provider anthropic

# Use Minimax (MiniMax-M2.7)
v100 run --provider minimax

# Use local Ollama
v100 run --provider ollama --model qwen3.5:9b

# Use plan-execute solver with replanning
v100 run --solver plan_execute --max-replans 3

# Enable sandbox execution
v100 run --sandbox

# Enable "Mission Control" TUI
v100 run --tui

# Name and tag runs for later querying
v100 run --name "parser refactor" --tag team=core --tag sprint=12
```

## Development checks

Use the repo-local lint wrapper so local lint matches CI:

```bash
GOCACHE="$PWD/.gocache" go test ./...
GOCACHE="$PWD/.gocache" go build ./...
GOCACHE="$PWD/.gocache" go vet ./...
./scripts/lint.sh
```

## Local Sandbox Image

For Docker sandbox runs, build the repo-local image that includes the binaries the harness expects:

```bash
./scripts/build-sandbox-image.sh
```

That image installs Go, `git`, `patch`, `ripgrep`, and `curl`, which avoids the missing-`patch` failure seen with a plain `golang` base image.

## Providers

### ChatGPT subscription (default)

Uses your existing ChatGPT Plus/Pro plan — no separate API billing. Authenticate with `v100 login` after filling `~/.config/v100/oauth_credentials.json`:

```bash
v100 login   # opens browser → completes OAuth → saves token
```

Model: `gpt-5.4`

### OpenAI API

Standard pay-as-you-go API access.

```bash
export OPENAI_API_KEY=sk-...
v100 run --provider openai --model gpt-4o
```

### Anthropic API

Claude API access via API key.

```bash
export ANTHROPIC_API_KEY=sk-ant-...
v100 run --provider anthropic --model claude-sonnet-4-20250514
```

Or authenticate interactively:

```bash
v100 login --provider anthropic   # prompts for API key, saves to ~/.config/v100/anthropic_auth.json
```

### Gemini subscription

Uses your existing Gemini Pro / Google One AI Premium plan — no separate API billing. Authenticate with `v100 login --provider gemini` after filling `~/.config/v100/oauth_credentials.json`:

```bash
v100 login --provider gemini
v100 run --provider gemini
v100 run --provider gemini --model gemini-2.5-pro
```

Models: `gemini-2.5-flash` (default), `gemini-2.5-pro`, `gemini-3-pro-preview`, `gemini-3-flash-preview`

### Minimax

Advanced model support via MiniMax-M2.7.

```bash
v100 login --provider minimax
v100 run --provider minimax
```

### Ollama (local)

Run fully local models via Ollama (no API key required).

```bash
ollama run qwen3.5:9b
v100 run --provider ollama --model qwen3.5:9b
```

### Provider matrix

| Provider | Auth | Default model | Streaming | Best for |
|----------|------|---------------|-----------|----------|
| `codex` | OAuth (`v100 login`) | `gpt-5.4` | yes | subscription-backed coding runs |
| `openai` | `OPENAI_API_KEY` | `gpt-4o` | yes | API-driven experiments |
| `anthropic` | `ANTHROPIC_API_KEY` or `v100 login --provider anthropic` | `claude-sonnet-4-20250514` | yes | Claude API experiments |
| `gemini` | OAuth (`v100 login --provider gemini`) | `gemini-2.5-flash` | yes | subscription-backed Gemini runs |
| `minimax` | OAuth (`v100 login --provider minimax`) | `MiniMax-M2.7` | yes | high-fidelity research runs |
| `ollama` | local daemon | `qwen3.5:2b` | yes | fully local runs |

OAuth client config for subscription providers lives at `~/.config/v100/oauth_credentials.json`.

### Known limitations

- Provider behavior differs noticeably; the same prompt can produce very different tool-use patterns across providers.
- Subscription providers require a local OAuth client config file before `v100 login` will work.
- Underspecified prompts can still trigger over-eager exploration on some models. Use `--budget-steps`, `--budget-tokens`, and default dangerous-tool confirmation when evaluating a new provider or prompt style.

## Solvers

v100 supports two solver strategies that control how the agent loop processes tasks.

### ReactSolver (default)

Classic ReAct (Reasoning + Acting) loop. The model receives the conversation, produces reasoning and tool calls, observes results, and repeats until done.

```bash
v100 run --solver react
```

### PlanExecuteSolver

Two-phase strategy: first generates a plan, then executes it with ReAct. If execution fails, the solver can replan and retry up to `--max-replans` times. Checkpoints are created before execution so the workspace can be restored on failure.

```bash
v100 run --solver plan_execute --max-replans 3
```

## Sandbox

v100 can execute tool operations inside an isolated Docker container instead of directly on the host.

### Setup

```bash
v100 run --sandbox
```

### Security

The Docker executor applies hardened security defaults:
- `--cap-drop ALL` — drops all Linux capabilities
- `--security-opt no-new-privileges` — prevents privilege escalation
- `--pids-limit 64` — limits child processes
- Seccomp profile blocking: mount, ptrace, kexec_load, and other sensitive syscalls
- Network isolation configurable via `network_tier`

### Configuration

```toml
[sandbox]
enabled = false
backend = "docker"              # "host" or "docker"
image = "ubuntu:22.04"
network_tier = "off"            # "off" (isolated) or "open" (bridge)
memory_mb = 512
cpus = 1.0
apply_back = "manual"           # "manual", "on_success", or "never"
```

### Snapshots and restore

During sandboxed runs, the solver can create workspace snapshots (checkpoints). These can be restored later:

```bash
# List checkpoints for a run
v100 restore --list <run_id>

# Restore to a specific checkpoint
v100 restore <run_id> <checkpoint_id>

# Resume from restored state
v100 resume <run_id>
```

### Apply-back

After a sandboxed run, changes can be merged back to the host workspace:
- `on_success` — automatically apply changes when the run ends successfully
- `manual` — prompt for confirmation
- `never` — keep changes only in the sandbox

## Commands

| Command | Description |
|---------|-------------|
| `v100 run` | Start interactive agent run |
| `v100 resume <run_id>` | Continue a run from its trace (`--auto --unsafe` supported) |
| `v100 restore <run_id> [checkpoint_id]` | Restore sandbox checkpoint |
| `v100 replay <run_id>` | Pretty-print run transcript |
| `v100 runs [-n N] [--provider X] [--failed] [--all]` | List recent runs with optional filtering |
| `v100 tools` | List registered tools |
| `v100 providers` | List configured providers |
| `v100 config init` | Write default config to `~/.config/v100/config.toml` |
| `v100 doctor` | Check provider auth, tools, run dir |
| `v100 login [--provider <name>]` | Authenticate via browser OAuth or API key |
| `v100 logout [--provider <name>]` | Remove stored auth token |
| `v100 score <run_id> <pass\|fail\|partial> [notes...]` | Score a completed run |
| `v100 distill <run_id>` | Distill a run trace into ShareGPT format |
| `v100 stats <run_id>` | Compute stats from trace events |
| `v100 metrics <run_id>` | Compute trace-derived efficiency/behavior metrics |
| `v100 compare <run_id> <run_id> [...]` | Compare multiple runs side-by-side |
| `v100 bench <bench.toml>` | Batch-run prompt/provider/model variants |
| `v100 experiment create <name>` | Create a new experiment |
| `v100 experiment run <exp_id> --prompt <text>` | Execute all experiment variants |
| `v100 experiment results <exp_id>` | Display statistical results |
| `v100 analyze <run_id>` | Automated behavioral analysis |
| `v100 digest <run_id>` | Compact failure digest for a completed run |
| `v100 mutate <run_id>` | Suggest improved prompt based on failure analysis |
| `v100 diff <run_a> <run_b>` | Find divergence point between traces |
| `v100 query [--tag k=v ...] [--score <verdict>]` | Filter runs by metadata |
| `v100 dev` | Rebuild/restart dev binary on `.v100-reload` |

### `v100 run` flags

```
--provider string              Provider name (codex, gemini, openai, ollama, anthropic, minimax)
--model string                 Model override
--solver string                Solver strategy: react (default), plan_execute
--max-replans int              Max replans for plan_execute solver
--sandbox                      Enable isolated sandbox execution
--streaming                    Enable real-time token streaming (default: true)
--budget-steps int             Max steps before halting
--budget-tokens int            Max tokens before halting
--budget-cost float            Max cost in USD before halting
--max-tool-calls-per-step int  Max tool calls per step
--confirm-tools string         always | dangerous | never (default: dangerous)
--auto                         Auto-approve all tool calls
--unsafe                       Disable path guardrails
--workspace string             Workspace directory for tool operations
--name string                  Human-readable run name (meta.json)
--tag key=value                Repeatable run tags (meta.json)
--temperature float            Sampling temperature
--top-p float                  Nucleus sampling parameter
--top-k int                    Top-k sampling parameter
--max-tokens int               Max output tokens per model call
--seed int                     Random seed for reproducibility
--exit                         Finalize and exit after initial prompt completes
--tui                          Enable "Mission Control" TUI
--tui-no-alt                   Disable alternate screen
--tui-plain                    Force monochrome rendering
--tui-debug                    Write TUI debug log in run directory
```

Default workspace is the current directory where `v100 run` is launched.

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

## Tools

| Tool | Danger | Description |
|------|--------|-------------|
| `fs_read` | safe | Read file contents |
| `fs_write` | **dangerous** | Write/append to file |
| `fs_list` | safe | List directory entries |
| `fs_mkdir` | safe | Create directory |
| `fs_outline` | safe | Semantic outline of functions/types (Go only) |
| `sh` | **dangerous** | Execute shell command |
| `git_status` | safe | Git status |
| `git_diff` | safe | Git diff |
| `git_commit` | **dangerous** | Stage and commit |
| `git_push` | **dangerous** | Push current branch |
| `sem_diff` | safe | Semantic entity-level diffing |
| `sem_impact` | safe | Impact analysis for specific code entities |
| `sem_blame` | safe | Entity-level blame for a file |
| `patch_apply` | **dangerous** | Apply unified diff |
| `project_search` | safe | Ripgrep search |
| `sql_search` | **dangerous** | Execute SQL against local SQLite |
| `graphviz` | safe | Render DOT files to images |
| `curl_fetch` | safe | Fetch URL content |
| `web_extract` | safe | Fetch a web page and return compact extracted signal |
| `agent` | **dangerous** | Spawn a bounded sub-agent run |
| `dispatch` | **dangerous** | Dispatch a task to a named agent role |
| `orchestrate` | **dangerous** | Coordinate multiple dispatches (fanout/pipeline) |
| `blackboard_read` | safe | Read shared run blackboard |
| `blackboard_write` | **dangerous** | Append/overwrite shared run blackboard |
| `reflect` | safe | Meta-cognitive self-critique and plan evaluation |
| `blackboard_search` | safe | Search vectorized blackboard memory |
| `blackboard_store` | **dangerous** | Store a record in vectorized blackboard |

Dangerous tools require confirmation unless `--auto` or `--confirm-tools never` is set.

## Config

Default location: `~/.config/v100/config.toml`

```toml
[providers.codex]
type = "codex"
default_model = "gpt-5.4"

[providers.openai]
type = "openai"
default_model = "gpt-4o"
[providers.openai.auth]
env = "OPENAI_API_KEY"

[providers.anthropic]
type = "anthropic"
default_model = "claude-sonnet-4-20250514"
[providers.anthropic.auth]
env = "ANTHROPIC_API_KEY"

[providers.ollama]
type = "ollama"
default_model = "qwen3.5:9b"
base_url = "http://localhost:11434"

[providers.gemini]
type = "gemini"
default_model = "gemini-2.5-flash"

[providers.minimax]
type = "minimax"
default_model = "MiniMax-M2.7"

[sandbox]
enabled = false
backend = "docker"
image = "ubuntu:22.04"
network_tier = "off"
apply_back = "manual"

[agents.researcher]
system_prompt = "You are a researcher agent. Find and read relevant code and return concise findings. Do not modify files."
tools = ["fs_read", "fs_list", "project_search"]
model = ""
budget_steps = 15
budget_tokens = 20000
budget_cost_usd = 0.0

[defaults]
provider = "codex"
solver = "react"
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
  trace.jsonl     # append-only event log (21 event types)
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
v100 compare <run_id> <run_id> [...]

# Query by metadata
v100 query --tag team=core --score pass

# Automated behavioral analysis
v100 analyze <run_id>

# Find where two runs diverged
v100 diff <run_a> <run_b>
```

### Batch benchmarks

```bash
v100 bench ./bench.toml
```

```toml
name = "prompt-rewrite-v1"

[[prompts]]
message = "Refactor parser for streaming mode."

[[variants]]
name = "codex-default"
provider = "codex"
model = "gpt-5.4"
budget_steps = 20
```

### Experiments

```bash
# Create an experiment with 3 repeats per variant
v100 experiment create my-experiment --repeats 3 \
  --variants gpt-4o:react --variants claude-sonnet-4-20250514:plan_execute

# Run all trials
v100 experiment run <exp_id> --prompt "Implement a linked list in Go"

# View results with statistical analysis
v100 experiment results <exp_id>
```

## Dogfooding

For a concrete operator loop with ten runnable tasks, sandbox drills, and a starter bench file, see [`dogfood/README.md`](dogfood/README.md) and [`dogfood/smoke.bench.toml`](dogfood/smoke.bench.toml).

## Multi-agent quick examples

```text
Call dispatch with {"agent":"researcher","task":"Find replay implementation and list key files."}
Call orchestrate with {"pattern":"fanout","tasks":[{"agent":"researcher","task":"Map replay"},{"agent":"reviewer","task":"List risks"}]}
Call blackboard_read with {}
```

## Debugging a run

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
| `Tab` | Cycle focus (input → transcript → trace → status) |
| `Alt+R` | Open Radio Station Selector |
| `Ctrl+M` | Toggle Visual Inspector Dashboard |
| `Ctrl+T` | Toggle trace pane |
| `Ctrl+S` | Toggle status pane |
| `Ctrl+A` | Copy full plain-text transcript |
| `Ctrl+Y` | Approve dangerous tool |
| `Ctrl+N` | Deny dangerous tool |
| `Ctrl+C` | Quit |

## Files created

`v100 config init` and the login flows create a small set of local files:

- `~/.config/v100/config.toml` — main runtime config
- `~/.config/v100/oauth_credentials.json` — local OAuth client config for Codex/Gemini
- `~/.config/v100/auth.json` — Codex subscription token after `v100 login`
- `~/.config/v100/gemini_auth.json` — Gemini subscription token after `v100 login --provider gemini`
- `~/.config/v100/minimax_auth.json` — Minimax auth token after `v100 login --provider minimax`
- `~/.config/v100/anthropic_auth.json` — Anthropic API key after `v100 login --provider anthropic`
- `runs/<run_id>/` — trace, metadata, and artifacts for each run

## License

[MIT](LICENSE)

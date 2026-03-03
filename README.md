# v100

A modular CLI/TUI agent harness built in Go. Pluggable state machine + append-only event log that runs an LLM agent loop with durable traces, confirmable tool execution, and budgets.

## Features

- **Durable traces** — every run is logged as append-only JSONL (`runs/<id>/trace.jsonl`)
- **Tool execution** — file system, shell, git, patch, ripgrep search
- **Dangerous tool confirmation** — CLI stdin prompt or TUI Ctrl+Y/Ctrl+N
- **Budget enforcement** — hard limits on steps, tokens, and cost
- **Resume & replay** — pick up any run from its trace; pretty-print transcripts
- **Two providers** — ChatGPT subscription (no API billing) or OpenAI API
- **Two UIs** — line-by-line CLI (default) or Bubble Tea 3-pane TUI (`--tui`)

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

Requires Go 1.21+. Optional: `rg` (ripgrep) for `project.search`, `patch` for `patch.apply`.

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

### `v100 run` flags

```
--provider string       Provider name (codex, openai)
--model string          Model override
--budget-steps int      Max steps before halting
--budget-tokens int     Max tokens before halting
--budget-cost float     Max cost in USD before halting
--confirm-tools string  always | dangerous | never (default: dangerous)
--auto                  Auto-approve all tool calls
--unsafe                Disable path guardrails
--tui                   Enable Bubble Tea TUI
--workspace string      Workspace directory for tool operations
```

## Tools

| Tool | Danger | Description |
|------|--------|-------------|
| `fs.read` | safe | Read file contents |
| `fs.write` | **dangerous** | Write/append to file |
| `fs.list` | safe | List directory entries |
| `fs.mkdir` | safe | Create directory |
| `sh` | **dangerous** | Execute shell command |
| `git.status` | safe | Git status |
| `git.diff` | safe | Git diff |
| `git.commit` | **dangerous** | Stage and commit |
| `patch.apply` | **dangerous** | Apply unified diff |
| `project.search` | safe | Ripgrep search |

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
```

## Run layout

```
runs/<run_id>/
  trace.jsonl    # append-only event log
  memory/        # injected into context each step
  workspace/     # tool working directory
  artifacts/
```

## TUI keybinds

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Tab` | Cycle focus (input → transcript → trace) |
| `Ctrl+T` | Toggle trace pane |
| `Ctrl+Y` | Approve dangerous tool |
| `Ctrl+N` | Deny dangerous tool |
| `Ctrl+C` | Quit |

## License

MIT

# v100

v100 is my engine for agentic research.

I use it to build, run, study, and evolve autonomous coding agents under real constraints. The core of the project is a Go-based agent runtime with a CLI, TUI, tool safety controls, durable memory, trace replay, benchmarking, eval, policy evolution, and long-running execution paths. Research is one feature of the system, but not the definition of it.

I built v100 to close the loop between idea, execution, observation, and iteration.

## What v100 is

v100 is the engine I use for agentic research work:

- running interactive agent sessions against real workspaces
- resuming and replaying runs to understand what happened
- evaluating runs, comparing them, and distilling traces into training data
- evolving agent policies against benchmark suites
- keeping durable memory and retrieval close to the agent runtime
- experimenting with tool use, safety boundaries, and provider routing
- running autonomous research loops when I want the system to execute experiments on its own

The `research` command is one feature inside that engine. It matters, but it is not the center of gravity of the repo.

## Current shape of the repo

The high-level mental model is:

- `cmd/v100/` contains the CLI surface
- `internal/core/` contains the run loop, solvers, tracing, checkpoints, and research orchestration
- `internal/tools/` contains built-in tools the agent can call
- `internal/providers/` wraps model backends behind one interface
- `internal/eval/` contains scoring, analysis, benchmarks, and experiment support
- `internal/memory/` contains durable memory and vector storage
- `internal/ui/` contains the terminal UI pieces
- a few Python files remain for experiment targets, but they are not the center of the system

## Main commands

The CLI surface is fairly broad now. The commands I reach for most often are:

- `v100 run` - start an agent run
- `v100 resume <run_id>` - resume a previous run
- `v100 replay <run_id>` - inspect a run trace as a transcript
- `v100 runs` - browse recent runs
- `v100 memory ...` - inspect and manage durable memory
- `v100 research --config research.toml` - run the autonomous research loop
- `v100 bench run <bench.toml>` - run benchmark suites
- `v100 analyze`, `v100 eval`, `v100 metrics`, `v100 diff`, `v100 verify` - inspect run behavior and outcomes
- `v100 evolve ...` - mutate and benchmark agent policy
- `v100 compress <run_id>` - force-compress long run histories
- `v100 wake ...` - run recurring autonomous wake cycles

## Recent development direction

Recent work has been concentrated in three areas:

- interactive reliability: fixing CLI confirmation freezes and raw-tty edge cases
- unattended execution: `--continuous` on `run` and `resume` for longer hands-off sessions
- retrieval and external context: ATProto indexing/recall and direct `user_posts` fetching from a user's PDS

That direction matches how I use the tool: longer runs, less babysitting, better recall, better observability.

## Install

Prebuilt releases are published on GitHub for Linux, macOS, and Windows. The release page also includes `checksums.txt`.

Installer scripts:

- macOS / Linux: `curl -fsSL https://raw.githubusercontent.com/tripledoublev/v100/main/scripts/install.sh | bash`
- Windows PowerShell: `irm https://raw.githubusercontent.com/tripledoublev/v100/main/scripts/install.ps1 | iex`

If you prefer to build from source:

```bash
go build ./cmd/v100
```

That gives you a local `v100` binary built from the current checkout.

## Quick start

### 1. Bootstrap config

```bash
v100 config init
v100 doctor
```

That writes the default config to the XDG config path and checks the local setup.

### 2. Start an interactive run

```bash
v100 run --provider codex --workspace .
```

Add `--tui` if you want the Bubble Tea interface instead of plain CLI streaming.

If you want unattended multi-step execution:

```bash
v100 run --provider codex --workspace . --continuous
```

### 3. Resume, inspect, and compare

```bash
v100 runs
v100 resume <run_id>
v100 replay <run_id>
v100 metrics <run_id>
```

## Research

`v100 research` is the subsystem for autonomous experiment loops.

It lets me define:

- the target file and context for the agent
- the experiment command and metric to parse
- setup and collect hooks for remote execution
- local or provider-backed compute
- round budgets and optional tracking integration

That is useful when I want the system to drive experiments on its own, but it remains one capability inside the broader engine.

## Tooling model

v100 treats tools as a first-class part of the runtime.

- tools are registered centrally and exposed to the model with schemas
- tools can be marked safe or dangerous
- dangerous tools can require confirmation
- reflective steps can be inserted before risky actions
- traces, checkpoints, and replay make tool behavior inspectable after the fact

This is one of the main reasons I use the project: I want agent autonomy, but I also want to see exactly how it behaves when the environment gets messy.

## Providers

The harness supports multiple model backends behind one interface, including:

- Codex
- OpenAI
- Anthropic
- Gemini
- GLM
- MiniMax
- Ollama
- llama.cpp

There is also separate embedding-provider support for retrieval tools, so I do not have to use the same backend for chat and vector indexing.

## Project structure

```text
cmd/v100/          CLI commands
internal/core/     loop, solvers, tracing, checkpoints, research
internal/tools/    tool implementations
internal/providers/ provider adapters
internal/eval/     scoring, benchmarks, experiments, analysis
internal/memory/   durable memory and vector stores
internal/ui/       terminal UI components
docs/              architecture notes and issue packs
research.toml      research loop configuration
```

## Notes on tone and scope

This is not meant to be a polished general-purpose framework in the abstract. It is my working engine for agentic research. I use it to try ideas quickly, keep the sharp edges visible, and evolve the system in public through actual use.

That means the repo sometimes carries a mix of:

- serious runtime and eval infrastructure
- rough-edged experimental features
- tooling that exists because I needed it last week

I think that is the right shape for this project.

## Development priorities

The highest-value areas to keep pushing on next are:

1. provider and tool integration reliability under long unattended runs
2. README and docs alignment so the public surface matches the actual product
3. eval and benchmark coverage for new runtime behaviors
4. research-loop ergonomics for remote and cloud-backed experiments
5. memory and retrieval quality, especially around external context sources

## License

MIT

# v100 Workflows

This document is the operator view of v100.

The point is simple: if you have the tool open and want to get work done, what should you run?

## 1. Interactive agent run

Use this when you want an agent to work in a real repository or workspace.

Typical starting point:

```bash
v100 run --provider codex --workspace .
```

Useful flags:

- `--tui` for the Bubble Tea interface
- `--continuous` to continue automatically between steps
- `--sandbox` to isolate execution
- `--confirm-tools` to control tool confirmations

Use this workflow for:

- interactive coding help
- repo exploration
- debugging with tool access
- supervised agent execution

## 2. Resume and inspect a run

Use this when you already have a run and want to continue it or understand what happened.

Typical commands:

```bash
v100 runs
v100 resume <run_id>
v100 replay <run_id>
```

Use `resume` when you want the agent to keep working.

Use `replay` when you want to inspect the trace without continuing execution.

Related commands:

- `v100 diff <run_id_a> <run_id_b>`
- `v100 blame <run_id> <file>`
- `v100 compress <run_id>`
- `v100 restore <run_id>`

## 3. Evaluate a run

Use this when a run is done and you want to score, classify, or analyze it.

Typical commands:

```bash
v100 metrics <run_id>
v100 analyze <run_id>
v100 eval <run_id> --rubric "..."
v100 verify <run_id>
```

What they are for:

- `metrics`: trace-derived runtime measurements and automatic classification
- `analyze`: behavioral analysis of the run
- `eval`: judge a run against a natural-language rubric
- `verify`: check run output against success invariants

Use this workflow for:

- post-run review
- model/provider comparison
- judging whether a run actually succeeded

## 4. Benchmark a policy or provider

Use this when you want controlled comparisons instead of one-off runs.

Typical command:

```bash
v100 bench run <bench.toml>
```

Use this workflow for:

- provider comparisons
- prompt/policy comparisons
- repeatable eval across variants

Related commands:

- `v100 bench bootstrap <name>`
- `v100 experiment create <name>`
- `v100 experiment run <experiment_id> --prompt "..."`
- `v100 experiment results <experiment_id>`

Rule of thumb:

- use `bench` for benchmark suites
- use `experiment` for structured repeated comparisons across variants and prompts

## 5. Durable memory workflow

Use this when you want the runtime to keep facts, preferences, constraints, or notes across runs.

Typical commands:

```bash
v100 memory list
v100 memory remember "..."
v100 memory show <id>
v100 memory forget <id>
```

Use this workflow for:

- preserving workspace-specific context
- recording recurring operator preferences
- keeping constraints available across sessions

Memory is for durable runtime context. It is not a substitute for replay, eval, or benchmark artifacts.

## 6. Long-running autonomous operation

Use this when you want less hands-on supervision.

Typical commands:

```bash
v100 run --provider codex --workspace . --continuous
v100 wake start
```

Use this workflow for:

- longer unattended runs
- recurring wake cycles
- background operation with checkpoints and traceability

Important controls:

- confirmations for dangerous tools
- sandboxing
- workspace guardrails
- replay and metrics after the fact

This mode is powerful, but it is where runtime reliability and operator discipline matter most.

## 7. Research subsystem

Use this when you want v100 to run autonomous experiment loops against a target file and an experiment command.

Typical command:

```bash
v100 research --config research/configs/research.toml
```

This workflow is for:

- repeated agent-driven edits
- experiment execution under a metric
- keep/discard style iteration
- local or remote experiment launching

`research/configs/research.toml` defines:

- the target file
- context files
- the experiment command
- metric parsing
- setup and collect hooks
- compute settings

This is an important subsystem, but it is not the whole product.

## 8. Training-loop subsystem

This is the specialized experiment-target path built around:

- `research/train-loop/prepare.py`
- `research/train-loop/train.py`
- `research/train-loop/program.md`

Use this only if you specifically want that training-loop workflow.

This path is optional and more specialized than the rest of v100. Most of the engine does not depend on it.

## 9. No GPU

You do not need a GPU for the main v100 engine.

GPU-independent workflows include:

- `run`
- `resume`
- `replay`
- `runs`
- `memory`
- `metrics`
- `analyze`
- `eval`
- `verify`
- `bench`
- `experiment`
- `wake`

The training-loop subsystem may require GPU-oriented setup depending on what experiment target you are running.

## 10. Choosing the right workflow

Use this shortcut if you are unsure where to start:

- I want an agent to work in my repo: `v100 run`
- I want to continue a previous run: `v100 resume`
- I want to inspect what happened: `v100 replay`
- I want to judge or compare outcomes: `v100 metrics`, `v100 analyze`, `v100 eval`, `v100 verify`
- I want repeatable comparisons: `v100 bench` or `v100 experiment`
- I want durable cross-run context: `v100 memory`
- I want unattended recurring operation: `v100 run --continuous` or `v100 wake`
- I want autonomous experiment loops: `v100 research`
- I want the specialized training target workflow: `research/train-loop/prepare.py`, `research/train-loop/train.py`, `research/train-loop/program.md`

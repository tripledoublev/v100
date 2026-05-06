# v100: Engine for Agentic Research

v100 is my engine for building, running, studying, and evolving autonomous coding agents under real constraints. 

It is not a framework in the abstract. It is a concrete Go-based agent runtime featuring a CLI, Bubble Tea TUI, tool safety controls, durable memory, trace replay, benchmarking, evaluation, policy evolution, and long-running execution paths.

I built v100 to close the loop between idea, execution, observation, and iteration.

---

## 🧠 Core Capabilities

v100 operates on a fundamental principle: **autonomy requires visibility, not hidden magic**. Every agent action is trackable, replayable, and inspectable.

### 1. Advanced Solvers & Routing
Instead of a rigid prompt loop, v100 implements multiple reasoning strategies (`Solvers`) that can be swapped or combined:
- **`react`**: The classic reasoning loop, enhanced with watchdogs for tool denial and stall recovery.
- **`plan_execute`**: A two-phase strategy where the agent previews a plan and executes it, with automatic replanning on failure.
- **`smartrouter`**: Cost-performance escalation. It routes "trivial" idempotent tool calls (like reading files) to cheap models (e.g., Gemini Flash or local Ollama) and escalates to frontier models (e.g., MiniMax, Claude Opus) when a dangerous or complex mutation is required.
- **`rlm`**: DSPy-style Recursive Language Model pattern with sub-model invocation.
- **`miniglm`**: Intelligent provider switching between tool-focused and reasoning-focused models.

### 2. Autonomous Loops
v100 is designed for long-running, unattended execution:
- **Wake Daemon (`v100 wake`)**: Runs on a recurring schedule. It can act as a `goal_generator` (mining TODOs and failures for next steps) or an `issue_worker` (autonomously picking open GitHub issues, implementing fixes, running local tests, pushing, and closing the issue).
- **Research Loop (`v100 research`)**: A fully autonomous experiment loop. It proposes code changes, runs remote (Modal) or local experiments, parses a metric, and implements keep/discard (git commit vs. revert) logic automatically.

### 3. Tooling & Safety
Tools are a first-class part of the runtime. The model interacts with the world through explicitly registered, schema-bound tools (40+ currently available):
- **Safety boundaries**: Tools are marked `Safe` or `Dangerous`. Dangerous tools can require explicit operator confirmation, trigger mandatory "Reflection" turns (`Policy.ReflectOnDangerous`) to assess confidence, or be blocked entirely.
- **Sandboxing**: Runs can be executed inside isolated Docker containers with strict **Network Tiers** to prevent unauthorized data exfiltration.
- **Semantic analysis**: Includes tools like `sem_diff`, `sem_impact`, and `sem_blame` that understand code entities (functions, classes) instead of just text lines.

### 4. Memory & External Context
v100 treats memory as runtime infrastructure, not just prompt stuffing:
- **Durable Blackboard**: A shared workspace for agents to read and write ongoing findings.
- **ATProto Integration**: Deep Bluesky integration for social context and provenance. Tools include `atproto_index`, `atproto_recall`, `atproto_vibe_check`, `atproto_daily_digest`, `atproto_graph_explorer`, `atproto_community_detect` (label-based community clustering), `atproto_follower_momentum` (new follower growth signals), `atproto_engagement_health` (posting cadence and engagement analytics), and `atproto_publish_provenance` (publishing signed provenance records to the AT Protocol network).

### 5. Observability & Trace Replay
If an agent does something surprising, you shouldn't have to guess why.
- Every run emits a structured `trace.jsonl`.
- `v100 replay <run_id>` lets you step through an agent's reasoning turn-by-turn after the fact.
- `v100 graph <run_id>` renders the trace as an interactive HTML DAG explorer.
- `v100 steer <run_id> <message>` injects an asynchronous steering message into an active run without interrupting it.
- `v100 trace export/import` converts traces to and from cross-harness formats (Inspect, METR).
- Checkpoints allow you to resume interrupted runs seamlessly.
- **Semantic taint isolation**: tool results from untrusted sources are wrapped with taint metadata to prevent prompt injection from propagating through reasoning steps.
- **Trajectory mirroring**: loop state snapshots are emitted at each turn for external metric collection and divergence detection.

---

## 🚀 Quick Start

### 1. Install & Bootstrap

Prebuilt releases are published on GitHub. Alternatively, build from source:

```bash
./scripts/build.sh
```

That rebuilds `./v100` and updates the shell `v100` link. The underlying Go command is:

```bash
go build -o v100 ./cmd/v100
```

Bootstrap your configuration and check your environment:

```bash
./v100 config init
./v100 doctor
```

### 2. Interactive & Unattended Runs

Start a standard interactive run using MiniMax (the preferred provider):

```bash
./v100 run --provider minimax --workspace .
```

Enable Bubble Tea TUI for a better visual experience:

```bash
./v100 run --provider minimax --tui --workspace .
```

Leverage advanced solvers for planning or cost routing:

```bash
./v100 run --solver plan_execute --plan --workspace .
./v100 run --solver smartrouter --workspace .
```

Run fully unattended (the agent executes until completion or budget exhaustion):

```bash
./v100 run --continuous --workspace .
```

### 3. Inspecting the Engine

Browse recent runs, resume them, or replay their traces:

```bash
./v100 runs
./v100 resume <run_id>
./v100 replay <run_id>
./v100 graph <run_id>              # Interactive HTML DAG of the trace
./v100 blame <run_id> <file_path>  # Which reasoning turn modified a file
./v100 steer <run_id> "message"    # Inject a steering message into a live run
./v100 trace export <run_id>       # Export trace as cross-harness JSON (Inspect/METR)
```

---

## 📂 Project Structure

```text
cmd/v100/          CLI commands
internal/core/     loop, solvers (react, plan, router), budgets, tracing, research, hooks
internal/tools/    40+ tool implementations (fs, git, web, atproto, semantic)
internal/providers/ provider adapters (MiniMax, GLM, Anthropic, Gemini, OpenAI, etc.)
internal/eval/     scoring, benchmarks, experiments, analysis
internal/memory/   durable memory and vector stores
internal/ui/       terminal UI components
docs/              architecture notes
research/          research configs and artifacts
```

## 📝 Notes on tone and scope

This is not meant to be a polished general-purpose framework in the abstract. It is my working engine for agentic research. I use it to try ideas quickly, keep the sharp edges visible, and evolve the system in public through actual use. 

That means the repo carries a mix of serious runtime infrastructure, rough-edged experimental features, and bespoke tooling. It is built for researchers and power users who want deep control over their autonomous systems.

## 📄 License

MIT

# v100 System Architecture Overview

v100 is designed as a modular, observable, and self-evolving agent harness. This document describes its core components and their interactions to guide both human developers and autonomous agents.

---

## 1. Core Execution Engine (`internal/core/`)

The heart of v100 is the **Loop**, which manages the lifecycle of an agent's run.

*   **`loop.go`**: Orchestrates the model + tool interaction cycle. It handles message history, budget enforcement, context compression, and the **Automatic Build Feedback Loop**.
*   **`registry.go`**: Maintains the list of available tools. Supports runtime registration of new tools.
*   **`trace.go`**: Emits 21+ structured event types to `trace.jsonl` for observability and replay.
*   **`snapshot.go`**: Manages sandbox checkpoints and state restoration.

---

## 2. Solvers (`internal/core/solver.go`)

Solvers implement the reasoning strategy for the loop.

*   **ReAct Solver**: Classic Reasoning + Acting loop.
*   **Plan-Execute Solver**: Generates a structured plan, then executes it with automatic re-planning on failure.

---

## 3. Providers (`internal/providers/`)

Providers wrap different LLM backends into a unified interface (`Metadata`, `Complete`, `Stream`).

*   **Subscription Providers**: Codex (ChatGPT Plus) and Gemini Pro. These utilize OAuth to bypass API costs.
*   **API Providers**: OpenAI and Anthropic.
*   **Local Providers**: Ollama.

---

## 4. Tools (`internal/tools/`)

Tools are Go implementations that the agent can invoke. 

*   **Static Tools**: Hard-coded capabilities like `fs_read`, `sh`, and `git_commit`.
*   **Dynamic Tools (`internal/tools/dynamic/`)**: Tools authored by agents at runtime during Phase 100 runs.
*   **Safety**: Tools are classified as `Safe` or `Dangerous`. Dangerous tools require a **Reflection Turn** or user confirmation.

---

## 5. User Interfaces

v100 provides two ways to interact with the agent loop:

*   **CLI**: Line-by-line streaming output, ideal for automation and logs.
*   **Mission Control TUI (`--tui`)**: A rich Bubble Tea-based dashboard featuring:
    *   **Transcript Pane**: Live agent reasoning and tool conversation.
    *   **Trace Pane**: Real-time event log for deep inspection.
    *   **Visual Inspector**: A "Gaming Minimap" style dashboard with entropy gauges for tokens, steps, and reasoning intensity.
    *   **Radio Station Selector**: Ambient background audio integration via `Alt+R`.

---

## 6. Evaluation & Self-Evolution (`internal/eval/`)

This layer turns trajectories into knowledge.

*   **`stats.go` / `metrics.go`**: Quantitative analysis of run efficiency.
*   **`distill.go`**: Converts JSONL traces into ShareGPT format for dataset generation and DPO.
*   **Reflective Scoring**: Nuanced evaluation rubrics for autonomous agents.

---

## 7. The Feedback Loop (The "Reality Check")

v100 implements a **Post-Mutation Build Hook** in `loop.go`. After any workspace-mutating tool (`fs_write`, `patch_apply`):
1.  The harness runs `go build ./...`.
2.  If it fails, the compiler output is injected as a **`SYSTEM ALERT`**.
3.  The agent must fix the error before proceeding, ensuring structural integrity.

---
*Document produced by v100 Research Harness - System Architecture v0.2.2*

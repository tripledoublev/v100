# v100 Phase 1 Specification: Epistemic Foundation

This document provides a technical specification for Phase 1 of the v100 roadmap. The goal is to enhance the agent's environment with structured memory, self-reflection, and semantic code understanding.

---

## 1. Vectorized Blackboard
**Objective:** Replace the flat `blackboard.md` file with a local vector database to support efficient, long-term state retrieval across multiple agents and long-horizon runs.

### Implementation Details:
*   **Storage:** Use a lightweight, local vector store (e.g., [ChromaDB](https://www.trychroma.com/) via a Go client or a simple embedded solution like [Flatbush](https://github.com/mourner/flatbush) / [Bleve](https://github.com/blevesearch/bleve) with custom embeddings).
*   **Embeddings:** Default to a local embedding model via Ollama (e.g., `nomic-embed-text`) or a standard OpenAI/Gemini embedding API call.
*   **Schema:**
    *   `run_id`: To scope memory to the current experiment.
    *   `content`: The raw text of the memory.
    *   `metadata`: `agent_role`, `step_id`, `timestamp`, `tags`.
*   **Tools:**
    *   `blackboard_search`: Takes a `query` string and returns the top-$k$ relevant memory fragments.
    *   `blackboard_store`: Takes `content` and optional `tags`. Automatically generates embeddings and persists to the store.
    *   `blackboard_read` (Legacy): Remains for compatibility, but reads a "summary" projection of the latest $N$ entries.

---

## 2. Uncertainty-Aware Tooling (Reflection Step)
**Objective:** Reduce "Tool Thrashing" and accidental destructive actions by forcing agents to reason about their certainty before executing high-stakes tools.

### Implementation Details:
*   **The "Reflection" Prompt:** Before executing any tool marked as `Dangerous`, the `internal/core/loop.go` will inject a hidden sub-turn:
    *   *"You are about to execute [TOOL_NAME] with [ARGS]. On a scale of 0.0 to 1.0, what is your confidence that this is the correct next step? If below 0.7, please state your primary uncertainty."*
*   **Threshold Logic:**
    *   **Confidence >= 0.8:** Proceed to `ConfirmFn` (user/TUI prompt).
    *   **0.5 <= Confidence < 0.8:** The harness automatically executes a "Validation Search" (e.g., `fs_read` or `project_search`) to confirm assumptions before re-prompting for the dangerous tool.
    *   **Confidence < 0.5:** The tool call is rejected with a system message: *"Low confidence detected. Suggest spawning a 'researcher' sub-agent to clarify requirements."*
*   **Integration:** Update `Loop.execToolCall` in `internal/core/loop.go` to handle this logic state machine.

---

## 3. Semantic Navigation (Tree-Sitter Integration)
**Objective:** Move from line-based text editing to entity-aware code manipulation.

### Implementation Details:
*   **Library:** Use [go-tree-sitter](https://github.com/smacker/go-tree-sitter) with support for Go, Python, TypeScript, and Rust.
*   **New Tools:**
    *   `fs_outline`:
        *   **Input:** `path`.
        *   **Output:** A list of functions, classes, and global variables with their line ranges and signatures.
    *   `fs_dependents`:
        *   **Input:** `path`, `entity_name`.
        *   **Output:** Uses a combination of Tree-Sitter and Ripgrep to find all call sites or usages of a specific entity within the workspace.
    *   `sem_patch`:
        *   **Input:** `path`, `function_name`, `new_body`.
        *   **Output:** Surgically replaces the body of a function while preserving comments and surrounding indentation, reducing "broken patch" errors.

---

## 4. Execution Plan for the Implementation Agent

1.  **Step 1: Scaffolding.**
    *   Install `go-tree-sitter` and the necessary language grammars.
    *   Create `internal/memory` package for the vector store abstraction.
2.  **Step 2: Vector Integration.**
    *   Implement `internal/memory/vector.go`.
    *   Update `internal/tools/blackboard.go` to use the new vector backend.
3.  **Step 3: Reflection Loop.**
    *   Modify `internal/core/loop.go` to include the confidence-check sub-turn.
    *   Add a `Confidence` field to `EventToolCall` in `internal/core/types.go`.
4.  **Step 4: Semantic Tools.**
    *   Implement `internal/tools/semantic.go` using Tree-Sitter.
    *   Register `fs_outline` and `fs_dependents` in the tool registry.
5.  **Step 5: Validation.**
    *   Run a test experiment using `v100 run --tag phase1_test`.
    *   Verify that `v100 metrics` shows a decrease in `tool_retry_rate`.

---
*End of Spec*

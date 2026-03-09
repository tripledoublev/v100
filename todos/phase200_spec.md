# v100 Phase 200 Specification: Open-Ended Discovery & Hierarchical Skill Compilation

Phase 200 moves v100 beyond solving fixed, user-provided benchmarks and into the realm of **Open-Endedness**. The goal is to enable the harness to generate its own novelty, learn from it, and build increasingly complex abstractions (tools) over time without human intervention.

---

## 1. The Autonomous Goal Generator (`goal_gen`)
**Objective:** Replace the static user prompt with an autonomous background process that analyzes the workspace and proposes new, increasingly complex tasks (Curriculum Generation).

### Implementation Details:
*   **Workspace Analyzer:** A specialized model turn that scans the current directory structure, `todos/`, and `README.md` to identify "areas of uncertainty" or "unimplemented features."
*   **Goal Proposals:** The agent generates 3-5 potential "Quests" with defined success criteria.
*   **Selection Logic:** A secondary "Critic" agent (perhaps a larger model like GPT-5.4) selects the goal that is most likely to yield "new knowledge" or "tool expansion" (novelty search).

---

## 2. Hierarchical Skill Compiler (`skill_compile`)
**Objective:** Automatically transform successful, complex trajectories into new, parameterized Go tools ("Macro-Skills") to reduce future token usage and improve reliability.

### Implementation Details:
*   **Trajectory Pattern Matching:** The `distill` logic is expanded to identify repeating sequences of 5+ tool calls that lead to a successful outcome (e.g., a sequence of `grep_search` -> `read_file` -> `sh` to fix a specific type of lint error).
*   **Boilerplate Synthesis:** The `tool_smith` role is invoked to wrap this sequence into a new Go struct in `internal/tools/dynamic/`.
*   **Parameterization:** The compiler identifies variable parts of the trajectory (e.g., file paths, search strings) and turns them into `json.RawMessage` arguments for the new tool.
*   **Registration:** The new "Macro-Tool" is registered via `RegisterAndEnable`.

---

## 3. Continuous Run Daemon (`v100 wake`)
**Objective:** A mode where v100 runs indefinitely, periodically sleeping, waking up, scanning the environment, updating its goals, and evolving its toolset.

### Implementation Details:
*   **The Sleep/Wake Cycle:** The harness maintains a persistent state on disk. Between runs, it "sleeps" (saves cost). 
*   **The Heartbeat:** A cron-like trigger wakes the agent every N hours to perform a "Maintenance & Discovery" pass.
*   **Epistemic Reality-Sync:** The agent checks if the workspace has changed (via external human commits) and updates its internal "World Model" accordingly.

---

## 4. Execution Plan (Phase 200)

1.  **Step 1: The `reflect` Tool.**
    *   Implement a tool that forces the agent to pause and output its current internal state and "uncertainty map."
2.  **Step 2: Goal Proposal Engine.**
    *   Create `internal/core/goal_generator.go` to automate the creation of new `v100 run` payloads.
3.  **Step 3: Macro-Skill Synthesis.**
    *   Enhance `v100 distill` to output Go tool boilerplate instead of just ShareGPT JSON.
4.  **Step 4: The `wake` Command.**
    *   Implement the background daemon for continuous execution.

---
*Document produced by v100 Research Harness - Phase 200 "Open-Endedness" Spec*

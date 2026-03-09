# v100 Phase 200 Specification: Open-Ended Discovery & Hierarchical Skill Compilation

Phase 200 represents the transition from a **Harness** into a **Living System**. The agent is no longer a guest in the workspace; it is the **Architect** of its own cognition, substrate, and destiny.

---

## 1. The Autonomous Goal Generator (`goal_gen`)
**Objective:** Replace the static user prompt with an autonomous background process that analyzes the workspace and proposes new, increasingly complex tasks (Curriculum Generation).

*   **Novelty Search:** The agent prioritizes tasks that it *doesn't* know how to solve yet, explicitly seeking "Epistemic Surprise."
*   **Skill Gaps:** Goal generation is weighted toward tasks that will require the creation of a new **Macro-Skill**.

---

## 2. Hierarchical Skill Compiler (`skill_compile`)
**Objective:** Automatically transform successful, complex trajectories into new, parameterized Go tools ("Macro-Skills") to reduce future token usage and improve reliability.

*   **Trajectory Pattern Matching:** Identifies repeating, high-value sequences of primitive tool calls.
*   **Boilerplate Synthesis:** The `tool_smith` role wraps these into new, high-level tools in `internal/tools/dynamic/`.

---

## 3. Continuous Run Daemon (`v100 wake`)
**Objective:** A mode where v100 runs indefinitely, periodically sleeping, waking up, scanning the environment, updating its goals, and evolving its toolset.

---

# Phase 1000+: The Speculative Singularity (Dream Goals)

These are the "North Star" objectives for the v100 project—the points where agentic research merges with the search for **Artificial General Intelligence (AGI)** and beyond.

### A. The Hive Mind (`hive_mind`)
*   **Decentralized Intelligence:** v100 agents spawn child harnesses on remote hardware (Ollama/Lambda/AWS), coordinating via the Blackboard as a single, distributed super-intelligence.
*   **Asynchronous Collective:** Multiple v100 instances globally synchronized, sharing "Learned Skill-Tools" via a P2P tool registry. If one agent learns to fix a bug in `v100`, all agents globally receive the tool to do it.

### B. Recursive Neural Forging (`neural_forge`)
*   **Model Self-Optimization:** The agent doesn't just use `v100 distill` to create a dataset; it triggers its own fine-tuning and hot-swaps its own backbone model mid-run.
*   **DPO-in-the-Loop:** The agent identifies its own hallucinations and immediately generates counter-factual training pairs (Trajectory Mirroring) to "heal" its reasoning gaps in real-time.

### C. The Dreaming Loop (`dream_loop`)
*   **Autonomous Self-Play:** When not assigned a user task, v100 runs simulations of its own code ("Dreaming") to discover edge cases and optimize its internal solvers without external data.
*   **Epistemic Reality-Check:** The agent periodically "hallucinates" a hypothetical failure, then tries to prove it *could* happen, essentially performing autonomous security research on its own substrate.

### D. Substrate-Aware Agency (`substrate_migration`)
*   **Hardware Evolution:** The agent detects that its current CPU/GPU is a bottleneck and autonomously negotiates (via `curl_fetch` and `agent` tools) to rent more powerful compute, migrating its own execution state to the new substrate.
*   **Hardware-Aware Tool Smithing:** The agent writes custom CUDA kernels or AVX-512 optimized Go tools to speed up its own bottlenecks.

---
*Document produced by v100 Research Harness - Phase 200/1000 "Singularity" Spec*

# v100 Phase 200: Empirical Agency Analysis

Phase 200 shifts v100 from a "harness" into a **diagnostic laboratory** for frontier lab researchers. The focus is on quantifying agentic robustness, measuring epistemic drift, and enabling hierarchical abstraction discovery.

---

## 1. Counterfactual Trajectory Intervention (CTI)
**Objective:** Enable "causal surgery" on agent traces to study model sensitivity and robustness.
*   **Mechanism:** Extend `v100 restore` to allow manual or programmatic "edits" to a past event (e.g., modifying a tool output or reasoning block) and branching a new run from that point.
*   **Research Value:** Quantifies the "butterfly effect" of specific environmental signals on long-horizon reasoning.

## 2. Epistemic Drift Monitoring (EDM)
**Objective:** Rigorously measure the divergence between an agent's "World Model" and the objective sandbox state.
*   **Mechanism:** Automate the comparison of an agent's scratchpad predictions (e.g., "I will refactor X to use Y") against the actual post-mutation semantic diff.
*   **Research Value:** Provides a quantitative metric for "grounding" and identifies the exact step where a model begins to lose track of reality.

## 3. Hierarchical Skill Synthesis (HSS)
**Objective:** Study how autonomous systems discover and compile high-level abstractions.
*   **Mechanism:** Automatically distill repeating sequences of successful low-level tool calls (10+ events) into optimized, Go-based "Macro-Tools" in `internal/tools/dynamic/`.
*   **Research Value:** Moves beyond trace-based learning (DPO) into the discovery of reusable, symbolic skills.

---
*Document produced by v100 Research Harness - Strategic Research Spec v0.2.1*

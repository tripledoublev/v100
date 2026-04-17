# v100 Phase 400: Resilience & Autonomy

Phase 400 hardens v100 from a **research harness** into a **production-grade autonomous operator**. The focus shifts from building evaluation infrastructure (Phase 300) to making the agent resilient, self-healing, and continuously improving.

---

## 1. Provider Resilience & Routing (PRR)
**Objective:** Eliminate single-provider failure modes and optimize cost/quality tradeoffs.
* **Mechanism:**
  - Provider health tracking: detect 429s, 500s, timeouts; auto-backoff with jitter
  - Fallback chains: configure `fallback = ["gemini", "openai", "minimax"]` per solver
  - Cost-aware routing: route simple tasks to cheap models, complex tasks to capable ones
  - Provider metrics: track latency, success rate, tokens/$ per provider per task type
* **Files:** `internal/providers/router.go` (new), `internal/providers/health.go` (new), config schema update
* **Research Value:** Studies model-provider diversity as a reliability engineering problem.

## 2. Context Window Intelligence (CWI)
**Objective:** Keep the agent effective across long sessions without hitting context limits.
* **Mechanism:**
  - Context pressure monitor: real-time `tokens_used / context_window` tracking
  - Proactive summarization: when saturation > 70%, emit a `SummarizeContext` event
  - Smart context pruning: drop old tool outputs while preserving reasoning chains
  - Compression budget: configurable max context percentage before forced summarization
* **Files:** `internal/core/context.go` (new), `internal/core/loop.go` (extend)
* **Research Value:** Tests whether proactive context management improves long-horizon task completion.

## 3. Continuous Evaluation Pipeline (CEP)
**Objective:** Make benchmarking a CI-like loop that catches regressions automatically.
* **Mechanism:**
  - `v100 bench watch`: long-running process that re-runs benches on code changes
  - Drift detection: compare latest scores against rolling baseline, alert on >10% regression
  - Score history: append-only JSONL log per bench, queryable with `v100 bench history <name>`
  - Trend visualization: `v100 bench trend <name>` prints ASCII sparkline of scores over time
* **Files:** `internal/eval/watcher.go` (new), `internal/eval/history.go` (new), `cmd/v100/cmd_eval.go` (extend)
* **Research Value:** Enables data-driven prompt and architecture decisions at agent-development speed.

## 4. Agent Self-Healing (ASH)
**Objective:** Let the agent detect and recover from its own failure modes mid-run.
* **Mechanism:**
  - Stuck detection: if 3+ consecutive tool calls produce no forward progress, inject a `reflect` turn
  - Error pattern matching: recognize common failure signatures (wrong tool args, missing files, rate limits) and auto-correct
  - Graceful degradation: if a tool fails 3x, disable it for the rest of the run and reroute
  - Checkpoint-based retry: on fatal error, restore from last good checkpoint with modified approach
* **Files:** `internal/core/loop.go` (extend), `internal/core/recovery.go` (new)
* **Research Value:** Studies autonomous error recovery as a meta-cognitive capability.

## 5. Dogfood Automation (DA)
**Objective:** Run the full 12-quest dogfood suite on every meaningful code change.
* **Mechanism:**
  - `v100 dogfood run [quest...]`: execute selected quests, collect scores
  - `v100 dogfood report`: print summary table of all quest results
  - Git integration: auto-tag runs with current commit hash
  - Regression alerts: flag any quest that goes from pass → fail
* **Files:** `cmd/v100/cmd_dogfood.go` (new), `internal/eval/dogfood.go` (new)
* **Research Value:** The dogfooding loop itself is the research — the agent tests itself.

---

## Dependency Order

```
PRR (1) ──┐
           ├──→ ASH (4) ──→ DA (5)
CWI (2) ──┘
CEP (3) ───────────────→ DA (5)
```

PRR and CWI are independent foundations. ASH depends on both. CEP is independent. DA ties everything together.

## Success Criteria

Phase 400 is complete when:
- [ ] A 429 from one provider auto-retries on the fallback within 5 seconds
- [ ] A 50-step run completes without context overflow
- [ ] `v100 bench history` shows 10+ data points with no manual intervention
- [ ] A stuck agent self-recovers at least once without operator input
- [ ] All 12 dogfood quests pass on the current codebase

---
*Document produced by v100 — Phase 400 Draft*

# v100 Issue Pack - 2026-05-06

This pack captures bugs and enhancement requests from the May 6, 2026 dogfooding session and architectural review.

## Priority Order

1. [Bug] Case-sensitivity in GLM provider model IDs
2. [Bug] smartrouter crashes on missing OpenAI key fallback
3. [UX] DAG HTML renderer clutters on streaming tokens
4. [UX] Tool list categorization and dispatcher
5. [UX] v100 doctor false positives on env vars
6. [Perf] Parallelized ATProto graphing
7. [UX] Visual "Steer" confirmation in TUI/CLI
8. [UX] Token budget "Soft Caps" warning
9. [UX] Interactive DAG "click-to-replay"
10. [UX] Continuous mode "Vibe Check" (v100 pulse)

---

## Issue 1: Case-Sensitivity in Providers (The "GLM Trap")

**Labels:** `Bug`, `P1: High`, `Providers`

### Problem
The GLM provider (and potentially others using OpenAI-compatible wrappers) is strictly case-sensitive regarding model identifiers. Configs using `GLM-5.1` fail, while `glm-5.1` works.

### Expected Behavior
Model identifiers should be normalized (lowercased) during configuration loading or provider initialization to prevent trivial configuration failures.

### Implementation
- Update `cmd/v100/helpers.go` to lowercase known provider model identifiers before passing them to the provider factory.

---

## Issue 2: smartrouter Fallback Failure

**Labels:** `Bug`, `P1: High`, `Infrastructure`

### Problem
If `smartrouter` is not explicitly configured with a `smart`/`cheap` tier in `config.toml`, it falls back to OpenAI by default. If `OPENAI_API_KEY` is not set, the run crashes with a cryptic "Unknown Model" error.

### Expected Behavior
`v100 doctor` should explicitly verify `smartrouter` dependencies. If a fallback is active but its required keys are missing, it should warn the user clearly.

---

## Issue 3: DAG HTML Renderer Token Grouping

**Labels:** `UX`, `P2: Medium`, `Visualization`

### Problem
The `v100 graph` command renders every event in the trace as a separate node. For streaming responses, this creates dozens of `model.token` nodes, making the DAG cluttered and unreadable.

### Expected Behavior
Contiguous `model.token` events from the same model call should be collapsed into a single node or merged into the preceding `model.call`/`model.response` node.

### Implementation
- Modify `buildTraceDAG` in `cmd/v100/cmd_graph.go` to group `model.token` events.

---

## Issue 4: Tool List Categorization & Dispatcher

**Labels:** `UX`, `Strategy`, `Context Efficiency`

### Problem
The current tool list is long and noisy, consuming significant context tokens.

### Proposal
Implement a tool categorization system (e.g., `atproto`, `files`, `network`). Instead of exposing 50+ tools, expose a few category dispatchers.
- **Trade-off:** Adds one reasoning turn but significantly reduces context noise for long-running sessions.

---

## Issue 5: v100 doctor False Positives

**Labels:** `Bug`, `P2: Medium`, `UX`

### Problem
`v100 doctor` occasionally reports environment variables as missing even when they are present in a `.env` file that was successfully loaded.

### Expected Behavior
`doctor` should accurately reflect the effective environment, including variables loaded from local `.env` files.

---

## Issue 6: Parallelized ATProto Graphing

**Labels:** `Performance`, `P2: Medium`, `ATProto`

### Problem
`atproto_community_detect` currently fetches profile data sequentially. Fetching follows for 50+ accounts is painfully slow.

### Expected Behavior
Use `golang.org/x/sync/errgroup` to fetch profile data in parallel with a concurrency limit (e.g., 5) to respect PDS rate limits.

### Implementation
- Refactor `internal/tools/atproto_graph.go`.

---

## Issue 7: Visual "Steer" Confirmation

**Labels:** `UX`, `P3: Low`, `TUI`

### Problem
When `v100 steer` is used to inject an instruction, the active agent run (in a background process or another terminal) gives no visual indication that it received the message.

### Expected Behavior
Add a `hook.intervention` UI event that prints a distinct banner in the TUI/CLI when a steering message is injected.

---

## Issue 8: Token Budget "Soft Caps"

**Labels:** `UX`, `P3: Low`, `Budgeting`

### Problem
The agent hits a hard wall when the token budget is reached, often leaving tasks unfinished.

### Expected Behavior
Inject a "Soft Cap" warning into the system prompt when 80% of the budget is consumed, advising the agent to wrap up its current task immediately.

---

## Issue 9: Interactive DAG "Click-to-Replay"

**Labels:** `UX`, `P3: Low`, `Visualization`

### Problem
The HTML DAG is currently static and used for inspection only.

### Expected Behavior
Enhance the DAG to allow clicking a node to launch `v100 replay` starting exactly at that reasoning turn.

---

## Issue 10: Continuous Mode "Vibe Check"

**Labels:** `UX`, `P3: Low`, `CLI`

### Problem
Long-running `--continuous` runs can drift, and operators currently have to tail full logs to see status.

### Expected Behavior
Implement `v100 pulse` command to provide a one-line summary of an active background agent's current activity.

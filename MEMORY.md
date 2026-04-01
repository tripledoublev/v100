## Commit Gate

- Before creating any commit in this repo, always run all three checks:
  - `./scripts/lint.sh`
  - relevant tests at minimum, and prefer `go test ./...` when feasible
  - rebuild the binary with `go build -o ./v100 ./cmd/v100`
- Only commit once those checks succeed.

## March 2026 UX & Provider Hardening

### 1. Extensive Benchmarking & Research
- Performed 30+ benchmark runs using `gemini-2.5-flash` and `MiniMax-M2.7` providers across various tasks and solver strategies.
- Verified the effectiveness of `plan_execute` for complex architectural analysis compared to standard `react` loops.
- Identified and prioritized several UX bottlenecks, leading to new GitHub issues (#84, #85).

### 2. Provider Hardening (MiniMax)
- **Error 2013 Fix**: Implemented `ensureToolResultContiguity()` in the Anthropic-format adapter. This ensures that tool results always immediately follow the assistant message containing their tool calls, even if system messages (like budget alerts) were interleaved.
- Added explicit diagnostic logging for MiniMax message ordering errors to speed up future debugging.

### 3. TUI "Mission Control" Evolution
- **Visual Inspector Dashboard**: Implemented a new "gaming-style" dashboard pane in the right column of the TUI. It provides real-time, color-coded meters for:
    - `STEPS`: Step budget consumption ("Fuel").
    - `TOKEN`: Context window saturation ("Entropy").
    - `REAS.`: Reasoning intensity via Input/Output token ratio.
    - `COST`: Real-time budget tracking.
- **Cognitive Heartbeat**: Added a dynamic ASCII pulse animation (`[──·Λ···──]`) that tracks agent activity.
- **Radio Station Selector**: Replaced manual station cycling with a dedicated modal (`Alt+R` or `/radio`) for selecting stations by name.
- **Station Updates**: Renamed "Radiojar" to "Radio Al Hara" and updated stream metadata fetching.
- **Typing Hygiene**: Removed conflicting keybindings (`n`, `p`, `1`) that previously interfered with text input.
- **Layout Math**: Refined the vertical budgeting logic in `view.go` to ensure Trace, Dashboard, and Status panes fit perfectly without overflow across terminal sizes.

### 4. New CLI Features
- **Non-Interactive Mode**: Added the `--exit` flag to `v100 run`. This allows the agent to execute a prompt and automatically finalize/exit once complete, enabling seamless automation and batch execution.

## Compression Mechanism Analysis

**Current behavior**

- Compression is triggered by `maybeCompress` once estimated prompt size crosses 75% of `ContextLimit`.
- The system now uses a two-pass strategy:
  - Pass 1: targeted compression of the largest non-recent messages.
  - Pass 2: oldest-half summarization as a fallback if the prompt is still too large.
- Tool results may also be truncated earlier via `MaxToolResultChars`, which reduces pressure before full compression kicks in.

**Tradeoffs that still remain**

- Compression still adds synchronous model calls, so it increases latency.
- Token estimation remains heuristic, so compression may happen slightly earlier or later than ideal.
- Dense summaries can still lose detail from verbose tool output, especially when the original content is highly structured.
- The quality of targeted compression depends on the selected compression provider and prompt quality.

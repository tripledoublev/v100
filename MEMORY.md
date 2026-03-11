## March 2026 UX & Provider Hardening

### 1. Extensive Benchmarking & Research
- Performed 30+ benchmark runs using `gemini-2.5-flash` and `MiniMax-M2.5` providers across various tasks and solver strategies.
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

**1) What triggers compression?**
Compression is triggered by the `maybeCompress` function when the estimated token count of the current messages (`l.Messages`) exceeds 75% of the `ContextLimit` defined in `l.Policy`. Additionally, at least 4 messages must be present for compression to occur. This function is called before a model inference turn, specifically within `ReactSolver.Solve` and `RouterSolver.Solve`.

**2) What gets compressed?**
When compression is triggered, the oldest half of the messages in the conversation history are sent to the LLM for summarization. The original oldest messages are then replaced by a single new system message containing the summary, followed by the remaining (newer) half of the original messages.

**3) What are the weaknesses of the current approach?**
*   **Coarse Granularity:** It always summarizes the oldest half, which might include recent, important context or miss opportunities to compress larger, older, less critical messages.
*   **Cost and Latency:** It involves an additional, synchronous LLM call, adding cost and delay.
*   **Potential Loss of Detail:** Summarization can lead to the loss of specific details, particularly from verbose tool outputs which often contain structured data critical for debugging or precise understanding.
*   **No Selective Compression:** It lacks the ability to target specific message types (e.g., large tool outputs) for compression while preserving others.
*   **Token Estimation Heuristic:** The `estimateTokens` function uses an approximation that might not be perfectly accurate for all content types or LLM providers.

**4) How could tool results be compressed individually?**
Individual tool results could be compressed by:
*   **Pre-processing tool outputs:** Implement tool-specific summarization logic within each tool or a generic mechanism that summarizes tool outputs exceeding a certain token threshold *before* they are added to `l.Messages`.
*   **Semantic Compression:** For structured outputs (like JSON), extract key information or filter irrelevant data to create a concise summary, potentially using a specialized parser or a smaller, faster model.
*   **Modified `maybeCompress`:** Adjust `maybeCompress` to identify and prioritize large tool result messages for individual summarization *before* applying the general conversation summarization, allowing them to remain in their original chronological position.
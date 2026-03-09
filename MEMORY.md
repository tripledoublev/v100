## Persistent Memory

### Compression Mechanism Analysis (internal/core/loop.go, internal/policy/policy.go)

**1) What triggers compression?**
Compression is triggered by the `maybeCompress` function when the estimated token count of the current messages (`l.Messages`) exceeds 75% of the `ContextLimit` defined in `l.Policy`. Additionally, at least 4 messages must be present for compression to occur. This function is called before a model inference turn, specifically within `ReactSolver.Solve` in `internal/core/solver_react.go` (line 30).

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

### Full Flow Trace: User Input -> Message Append -> Model Call -> Tool Calls -> Results Stored

The `ReactSolver.Solve` method (in `internal/core/solver_react.go`) orchestrates the core agent loop.

1.  **User Input:** The `ReactSolver.Solve` function is invoked with the user's initial prompt (`userInput`).

2.  **Message Append: User Message:**
    *   **Location:** `internal/core/solver_react.go`, line 26.
    *   `l.Messages = append(l.Messages, providers.Message{Role: "user", Content: userInput})`
    *   The user's input is immediately added to the `l.Messages` history as a message with `Role: "user"`.

3.  **Optional Compression:**
    *   **Location:** `internal/core/solver_react.go`, line 30.
    *   `_ = l.maybeCompress(ctx, stepID)`
    *   If `l.Messages` token count exceeds 75% of `ContextLimit` and has at least 4 messages, the oldest half is summarized by an LLM call and replaced with a single `system` message containing the summary. This modifies `l.Messages`.

4.  **Model Call Preparation:**
    *   `l.buildMessages()` is called, which gathers the system prompt, persistent memory, and the current `l.Messages` to create the full context for the LLM. This assembled list is *not* `l.Messages` itself but a temporary slice sent to the provider.

5.  **Model Inference Call:**
    *   `l.Provider.Complete(...)` or `streamer.StreamComplete(...)` is invoked, sending the prepared `msgs` to the LLM.

6.  **Model Response Processing:**
    *   The LLM returns `AssistantText` and/or `ToolCalls`.

7.  **Message Append: Assistant Message:**
    *   **Location:** `internal/core/solver_react.go`, lines 128-132 (after streaming logic) or 105-109 (after non-streaming logic).
    *   `l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: assistantText.String(), ToolCalls: toolCalls})`
    *   The LLM's response (text and/or tool calls) is added to `l.Messages` as an `assistant` role message.

8.  **Tool Call Execution Loop:**
    *   If the model's response included `toolCalls`, the `ReactSolver.Solve` function iterates through them, calling `l.execToolCall` for each.

9.  **Message Appends within `l.execToolCall` (Tool Results):**
    *   **User Denied Tool Execution:**
        *   **Location:** `internal/core/loop.go`, lines 142-149.
        *   `l.Messages = append(l.Messages, providers.Message{Role: "tool", Content: "user denied tool execution", ToolCallID: tc.ID, Name: tc.Name})`
        *   If a dangerous tool is denied by the user, a `tool` role message is added.
    *   **Low Confidence Reflection Rejection:**
        *   **Location:** `internal/core/loop.go`, lines 125-133.
        *   `l.Messages = append(l.Messages, providers.Message{Role: "tool", Content: "ERROR: " + msg, ToolCallID: tc.ID, Name: tc.Name})`
        *   If the agent's self-reflection indicates low confidence in a dangerous tool, a `tool` role error message is added.
    *   **Tool Execution Result:**
        *   **Location:** `internal/core/loop.go`, lines 270-277.
        *   `l.Messages = append(l.Messages, providers.Message{Role: "tool", Content: content, ToolCallID: tc.ID, Name: tc.Name})`
        *   The output (or error message) from the tool's execution is appended to `l.Messages` as a `tool` role message.

This cycle (Model Call -> Assistant Message -> Tool Calls -> Tool Results) continues until the model produces a response without any `ToolCalls`.

### Run Summary at Exit (cmd/v100/cmd_run.go)

The run summary is a post-processing step executed at the end of the `runWithCLI` function (and similarly in `runWithTUI`).

1.  **Trigger:** It's triggered when the main interaction loop exits (e.g., user quits, budget exceeded).
2.  **Condition:** A summary is generated only if `len(loop.Messages) > 1`, ensuring there's meaningful conversation history.
3.  **Dedicated LLM Call:**
    *   A new `context.Context` with a 10-second timeout is created.
    *   A separate LLM provider is built for summarization, specifically configured for a "gemini" provider and "gemini-2.5-flash" model, regardless of the primary model used for the run.
4.  **Summary Prompt:** The entire `loop.Messages` conversation history is sent to this dedicated summarization model, along with an explicit user message: `"Briefly summarize the outcome of this run in one sentence (max 20 words). What was achieved?"`
5.  **Result:** The `AssistantText` from this summarization LLM call is extracted, trimmed, and stored as `finalSummary`.
6.  **Emission:** The `finalSummary` is then included in the `EventRunEnd` trace event emitted by `loop.EmitRunEnd`. This provides a concise overview of the run in the trace file for later analysis.

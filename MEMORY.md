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
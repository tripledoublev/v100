# v100 UX Research & Feature Exploration Report
**Date:** May 6, 2026

## 💎 The "Gems" of v100
During this research phase, we've identified several standout features that provide deep observability and autonomy:

1.  **Observability Stack (`graph`, `analyze`, `blame`):**
    *   `v100 graph`: Generates interactive HTML DAGs of reasoning.
    *   `v100 analyze`: Automated behavioral analysis with efficiency scoring.
    *   `v100 blame`: Precise file-level provenance linking code changes to specific reasoning turns.
2.  **Autonomous Integrity (`dogfood`):**
    *   The built-in test harness (`dogfood run`) allows for rapid validation of runtime behavior across different LLM providers (MiniMax, GLM, Gemini).
3.  **Economic Autonomy (`smartrouter`):**
    *   Intelligent routing between cheap discovery tiers and frontier execution tiers.
4.  **Live Steering (`steer`):**
    *   The ability to inject guidance into an active run trace without interruption.

## 🛠️ ATProto Algorithm Analysis
**Feature:** `atproto_community_detect`
*   **Algorithm:** Greedy Shared-Neighbor Clustering.
*   **Performance:** identified as sequential.
*   **Recommendation:** Parallelize `getFollows` fetches to handle large social graphs efficiently.

## 🩹 Technical Friction & Resolutions
*   **GLM Capitalization:** Discovered that the GLM provider (OpenAI-compatible) is case-sensitive. `GLM-5.1` returned a 400 error; `glm-5.1` is the correct identifier.
*   **Doctor Diagnostics:** Improved understanding of how `v100 doctor` verifies environment variables vs config defaults.
*   **SmartRouter Hardening:** Confirmed that `smartrouter` needs explicitly configured tiers to avoid defaulting to unconfigured providers like OpenAI.

## 🚀 Conclusion
The v100 engine is a robust, highly observable agent runtime. It excels at providing researchers with the tools needed to "close the loop" between agent execution and analysis.

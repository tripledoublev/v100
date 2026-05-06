# GEMS — Key Capabilities of v100

> Each "gem" represents a core capability that distinguishes the system.

## Gem 1 — ATProto-Native Social Graph

Full Bluesky/AT Protocol integration: feed reading, posting, replies, quotes,
image uploads, notifications, and social-graph exploration — all driven by
first-party XRPC calls with no external service dependencies.

## Gem 2 — Pluggable Specialist Dispatch

The `dispatch` / `orchestrate` layer fans work out to named specialist agents
(coder, researcher, reviewer) in pipeline or fan-out patterns, enabling
multi-model, multi-step workflows from a single invocation.

## Gem 3 — Persistent Semantic Memory

Indexed ATProto records and workspace artefacts are embedded and stored
locally for later recall via `atproto_recall`.  This gives the agent a
growing, searchable memory that survives restarts and spans sessions.

## Gem 4 — Autonomous Dogfooding

The ability to run self-test suites to ensure runtime integrity across
multiple providers.  The agent executes its own test harnesses, inspects
results, and surfaces regressions — acting as both author and first consumer
of its quality gate.  Cross-provider runs validate that behaviour is
consistent regardless of which LLM backend is active.

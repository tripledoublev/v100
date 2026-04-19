## CWI Implementation Plan (2025-04-18)

### Context Window Intelligence — changes needed:

1. `internal/core/types.go` — Add `ContextPressure float64` and `ContextWindowSize int` to `LoopState`; add `EventContextPressure` event type and `ContextPressurePayload` struct
2. `internal/core/context.go` (new) — `PressureMonitor` as a `PolicyHook` that emits warning and triggers `ForceCompress` at configurable saturation threshold (default 70%)
3. `internal/core/loop.go` — Populate new `LoopState` fields in `runHooks`; wire `estimateTokens` + `ModelMetadata.ContextSize`
4. `internal/policy/policy.go` — Add `PressureThreshold float64` field (default 0.70)
5. `internal/core/context_test.go` — Unit tests

### Key facts:
- `estimateTokens(msgs)` already exists in loop.go
- `ModelMetadata.ContextSize` is populated on Loop struct
- `Loop.Policy.ContextLimit` is existing compression threshold
- `ForceCompress(ctx, stepID)` already exists
- `PolicyHook func(state LoopState) HookResult` is the hook interface
- `runHooks()` in loop.go builds `LoopState` — need to add pressure fields there

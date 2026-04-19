# MEMORY — v100 Development Log

## 2025-04-18: Context Window Intelligence (CWI) — COMPLETE ✓
- Branch: main (commit aad0319)
- **Phase 400, Item 2 of 5** done
- `internal/core/context.go` — `PressureMonitor(threshold)` PolicyHook
- `internal/core/context_test.go` — 8 tests, all passing
- `internal/core/types.go` — `EventContextPressure`, `ContextPressurePayload`, `LoopState` pressure fields
- `internal/core/loop.go` — pressure computed in `runHooks`, lazily wired from policy
- `internal/policy/policy.go` — `PressureThreshold float64` field

### Phase 400 Status:
- ✅ 1. Provider Resilience (PRR)
- ✅ 2. Context Window Intelligence (CWI)
- ❌ 3. Continuous Eval Pipeline (CEP) — bench watch/history/trend
- 🟡 4. Agent Self-Healing (ASH) — watchdogs exist, no recovery.go
- ❌ 5. Dogfood Automation (DA)

## 2025-04-18: Tool Detail Pane — COMPLETE ✓
- 3rd column in TUI for inspecting tool call details (Ctrl+D / click)

## Previous: Image Rendering — COMPLETE ✓

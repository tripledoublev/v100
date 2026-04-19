# MEMORY — v100 Development Log

## 2025-04-18: Three Phase 400 items shipped

### CWI — Context Window Intelligence ✓ (aad0319)
- `internal/core/context.go` — `PressureMonitor` PolicyHook
- Warns at 70% context saturation, forces compress at 80.5%

### ASH — Agent Self-Healing ✓ (258fd3b)
- `internal/core/recovery.go` — `RecoveryHook` with stuck detection, error pattern matching, graceful degradation
- Auto-wired into all runs via lazy hook init

### CEP — Continuous Eval Pipeline ✓ (846ac91)
- `internal/eval/history.go` — `LoadHistory`, `Sparkline`, `FormatHistoryTable`, `FormatTrendSummary`
- `v100 bench history <name>` — table of all runs with scores
- `v100 bench trend <name>` — sparkline + drift detection (10% regression alert)
- `--runs` flag for custom runs directory

### Phase 400 Status:
- ✅ 1. Provider Resilience (PRR) — v0.2.18
- ✅ 2. Context Window Intelligence (CWI)
- ✅ 3. Continuous Eval Pipeline (CEP)
- ✅ 4. Agent Self-Healing (ASH)
- ❌ 5. Dogfood Automation (DA)

## 2025-04-18: Tool Detail Pane ✓
- 3rd column TUI, Ctrl+D / click to inspect tool calls

# v100 Spec — Multi-Gateway, Profiles & Personality (v0.4)

Date: 2026-06-29
Milestone: `v0.4 — Multi-Gateway, Profiles & Personality` (#2)
Author: tech-lead pass

## Vision

Today `v100 gateway telegram` is a personal, single-transport, single-profile
bridge: every message goes straight to one ACP-backed agent with one fixed
provider and the globally-enabled toolset. There is no per-chat control, no tool
sandboxing per gateway, and no second transport.

v0.4 turns this into a **deployable bot platform**:

- A **transport-agnostic gateway core** that both Telegram and a new **Signal**
  transport reuse.
- **Profiles** that bind a gateway (and optionally a specific chat) to a
  restricted toolset, provider/model/solver, system prompt, network tier, and
  budget — so it is *safe* to hand a bot to a friend.
- **In-chat slash-commands** (`/model`, `/solver`, `/provider`, `/profile`,
  `/reset`, `/help`, `/whoami`) so the operator can reconfigure a chat live.
- **ACP per-session overrides** so model/solver/provider switching is real, not
  a child-process restart.
- **More TUI themes**, plus two **new gateway-relevant tools** (`translate`,
  voice replies via a TTS shim).
- The **flagship deliverable**: a ready-to-deploy **personal chat persona
  (Vincent)** Signal profile + preset that answers naturally in québécois
  French with read-only background tools, locked down so a friend can chat
  with it safely.

## Current-state anchors (verified 2026-06-29)

- Telegram gateway: `cmd/v100/cmd_gateway_telegram.go` (1047 lines).
  - `handleTelegramMessage()` (`:502`) dispatches *all* text to ACP — no command
    parsing.
  - ACP child spawned in `runACPServer()` (`:260`) with a fixed `--provider`.
  - Per-chat state: `telegramGatewaySession` (`:82`), keyed `tg-<chatID>`.
- Config: `internal/config/config.go`. `TelegramConfig` (`:49`), `UIConfig.Theme`
  (`:68`), `ToolsConfig` (`:150`), `DefaultsConfig.Solver` (`:206`). No gateway
  profile concept.
- ACP: `internal/acp/protocol.go`. `SessionNewParams` (`:172`) only carries
  `CWD`/`RunDir`. `SessionResumeParams` (`:206`) *already* has `Provider`/`Model`
  fields — precedent for threading overrides. `cmd/v100/cmd_acp.go` builds the
  loop per `session/new` from config + `applyProviderOverride`.
- Tools: `internal/tools/registry.go`. Allowlist enforced via `enabled` map;
  `web_search` and `wiki` provide background fact-checking; `atproto_feed`/
  `atproto_notifications` read the Bluesky timeline. 48 tools registered.
- Themes: `internal/ui/theme.go`. 4 built-ins (`v100`, `mono`, `dracula`,
  `catppuccin`); `builtinThemes` map + `ThemeByName`/`ThemeNames`. Test
  `theme_test.go` asserts the exact name set — must update when adding themes.
- Solvers: `react`, `plan_execute`, `router`, `dual_channel`, `rlm`, `miniglm`.

## Workstream / issue map

Dependency order (→ = depends on):

```
A. Gateway core extraction (internal/gateway)        [P1, Infra]   foundation
B. ACP per-session provider/model/solver overrides   [P1, Infra]   foundation
C. Gateway slash-command control plane               [P1, Infra]   → A, B
D. Gateway profiles (tool/setting sandbox)           [P0, Safety]  → A
E. Signal gateway                                    [P1, Infra]   → A, D
F. Quebec French news Signal bot preset + e2e        [enhancement] → D, E, (H)
G. More TUI themes                                   [P2, TUI]     independent
H. New tool: translate                               [P2, enh]     independent
I. Gateway voice replies (TTS shim)                  [P2, enh]     → A
```

Every issue follows **red/green TDD**: write the failing test first, then the
implementation, then refactor. Each carries a Definition of Done and explicit
verification steps (`./scripts/lint.sh`, `go test ./...`, `go build ./...`).

See the per-issue bodies on GitHub (milestone #2) for full implementation plans.
</content>

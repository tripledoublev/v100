# v100 Memory

## 2026-04-16
- ATProto index+recall pipeline working (feed, notifications, user_posts, profile)
- URI dedup DONE: `VectorStore.HasTag` + check in `internal/tools/atproto_rag.go:115`
- ~287 records indexed; dedup prevents growth from repeat indexing

## 2026-04-17 — Phase 300 COMPLETE ✅ / Phase 400 in progress
- Phase 300 all 4 items shipped (RSE, TPM, SBB, CGE)
- Phase 400 spec written to `todos/phase400_spec.md` — 5 items

### Phase 400 Progress

**#1 PRR (Provider Resilience & Routing) — SHIPPED ✅**
- `internal/providers/health.go` + `resilient.go`: HealthTracker + ResilientProvider (already existed)
- `internal/config/config.go`: added `Fallbacks []string` to ProviderConfig
- `cmd/v100/helpers.go`: wired `buildProviderFromConfig` → wraps with `ResilientProvider` when fallbacks configured
- `cmd/v100/cmd_admin.go`: `providers health` subcommand + fallbacks shown in `providers` output
- `internal/config/config.go`: default anthropic → [gemini, openai] fallback chain
- `go get github.com/PuerkitoBio/goquery` — fixed missing dep

**#2 CWI**: compression logic exists in `loop.go`, no real-time TUI meter yet
**#3 CEP**: bench scaffold exists, no `bench watch` / drift detection / history
**#4 ASH**: not started
**#5 DA**: configs in `dogfood/`, no `v100 dogfood run/report` commands

Next: operator picks next item OR runs dogfood quests.

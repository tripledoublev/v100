# v100 Memory

## 2026-04-15
- User handle: charlebois.info (DID: did:plc:y3lae7hmqiwyq7w2v3bcb2c2)
- Building 3 new ATProto graph tools: `atproto_get_follows`, `atproto_get_followers`, `atproto_get_profile`
- File: `internal/tools/atproto_graph.go` (new) + `atproto_graph_test.go` (new)
- Pattern: same as existing tools in atproto.go — struct with ATProtoConfig, Safe, NeedsNetwork
- Public API endpoint: `public.api.bsky.app` works for unauthenticated graph queries
- Bluesky PDS endpoint: `bsky.social` requires auth
- ATProto tools already exist: feed, notifications, post, resolve, vibe_check, daily_digest
- User is member-worker @hypha.coop, runs spores.garden and couleurs.bsky.social

## 2026-04-16
- BUG FIX: CLI mode ConfirmTool freezes — `bufio.NewScanner(os.Stdin)` blocks in raw/cooked mode conflicts
- Fix: rewrite ConfirmTool to use `term.MakeRaw` + direct byte reads (same as promptTerminal)
- Key file: `internal/ui/cli.go` lines 407-436
- ConfirmTool is called via `buildConfirmFn()` in `cmd/v100/helpers.go` and `cmd/v100/cmd_resume.go`
- ENHANCEMENT: Added `user_posts` source to `atproto_index` tool
  - Fetches posts directly from a user's PDS via `com.atproto.repo.listRecords`
  - Bypasses appview entirely — works even when bsky.social is down
  - Resolves handle → DID → PDS endpoint via PLC directory
  - Three fallback handle resolution: appview XRPC, well-known, DNS-over-HTTPS
  - File: `internal/tools/atproto_rag.go`

# v100 Memory

## 2026-04-15
- User handle: charlebois.info (DID: did:plc:y3lae7hmqiwyq7w2v3bcb2c2)
- Building 3 new ATProto graph tools: `atproto_get_follows`, `atproto_get_followers`, `atproto_get_profile`
- File: `internal/tools/atproto_graph.go` (new) + `atproto_graph_test.go` (new)
- Pattern: same as existing tools in atproto.go — struct with ATProtoConfig, Safe, NeedsNetwork
- Public API endpoint: `public.api.bsky.app` works for unauthenticated graph queries
- Bluesky PDS endpoint: `bsky.social` requires auth
- Still need to find tool registration wiring (where RegisterAndEnable is called)
- ATProto tools already exist: feed, notifications, post, resolve, vibe_check, daily_digest
- User is member-worker @hypha.coop, runs spores.garden and couleurs.bsky.social

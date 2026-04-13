## Commit Gate

- Before creating any commit in this repo, always run all three checks:
  - `./scripts/lint.sh`
  - relevant tests at minimum, and prefer `go test ./...` when feasible
  - rebuild the binary with `go build -o ./v100 ./cmd/v100`
- Only commit once those checks succeed.

## In Progress: news_fetch RSS bug fixes (April 2026)

### 3 bugs found in news_fetch sources:
1. **TVA Nouvelles**: raw `&x=0` in `<media:content>` URLs breaks Go's strict XML parser. Fix: add `sanitizeXMLBody()` that escapes bare `&` to `&amp;` before parsing.
2. **L'Actualité**: RSS parses fine but `filterFreshNewsItems()` drops all items because they're >24h old and have valid timestamps. The fallback only catches "unknown time" items. Fix: when all items are filtered, return the N most recent items regardless.
3. **Radio-Canada**: `ici.radio-canada.ca/regions/quebec/` is a JS SPA — empty HTML shell, no RSS feed available. Page extraction returns nothing. Will try `ici.radio-canada.ca/info` or a different approach.

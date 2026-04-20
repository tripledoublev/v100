# MEMORY — v100 Development Log

## 2025-04-19: Session — cleanup, test fixes, trace truncation

### Commits this session (6 total):
- `d0d4fdb` — fix missing goquery transitive dep (build fix)
- `5ed9c06` — fix compression test I/O explosion (5000→600 char messages)
- `f5f7d17` — remove 4 placeholder images from repo root
- `f895939` — gitignore root-level image files
- `2336335` — feat: trace payload truncation (256/1024/2048 char caps)
- `e61f565` — chore: remove trace.go.orig backup

### Issue pack status (docs/issue-pack-2026-03-10.md):
All 4 issues already resolved in codebase:
1. `project_search` false negatives → ✅ exit code handling + tests
2. `contains` scorer case sensitivity → ✅ ToLower both sides + tests
3. Denial retry loops → ✅ per-key counting, stop at 2, cap at 5 total
4. Exact-write verification → ✅ FileContent scorer exists

### Test health:
- All packages pass: `go test ./internal/...` OK
- Core tests: 71ms
- `/tmp` cleaned from 22G → 860M

### Trace truncation (new in `2336335`):
- `ModelCallPayload` messages → 256 chars max per message
- `ModelRespPayload` text → 1024 chars max
- `ToolResultPayload` output → 2048 chars max
- Max line size: 10MB (was 1MB, causing scan failures)
- This will significantly reduce `runs/` directory growth

### Current version: v0.2.19
### Phases complete: 300, 400
### Next: Plan Phase 500 or tackle new features
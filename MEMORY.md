# MEMORY — v100 Development Log

## 2025-04-19: Session cleanup & test fixes

### Commits this session:
- `d0d4fdb` — fix missing goquery transitive dep (build fix)
- `5ed9c06` — fix compression test I/O explosion (5000→600 char messages)
- `f5f7d17` — remove 4 placeholder images from repo root

### Issue pack status (docs/issue-pack-2026-03-10.md):
All 4 issues already resolved in codebase:
1. `project_search` false negatives → ✅ exit code handling + tests
2. `contains` scorer case sensitivity → ✅ ToLower both sides + tests
3. Denial retry loops → ✅ per-key counting, stop at 2, cap at 5 total
4. Exact-write verification → ✅ FileContent scorer exists

### Test health:
- All packages pass: `go test ./internal/...` OK
- Core tests: 91ms (was timing out before due to 5000-char test data filling /tmp)
- `/tmp` cleaned from 22G → 860M

### Current version: v0.2.19
### Phases complete: 300, 400
### Next: Plan Phase 500 or tackle new features

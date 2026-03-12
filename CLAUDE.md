# v100 — Claude Code Guide

## Project overview

v100 is a terminal-native AI agent harness written in Go. It orchestrates LLM providers (Gemini, MiniMax, Anthropic, Codex, OpenAI, Ollama) with a pluggable tool system, multi-solver architecture (ReAct, plan_execute, router), budget tracking, and trace-based observability.

**Module:** `github.com/tripledoublev/v100`
**Binary:** `./v100_binary` (gitignored; rebuild with `go build -o v100_binary ./cmd/v100/`)
**Config:** `~/.config/v100/config.toml` (bootstrap with `v100 config init`)
**Auth tokens:** `~/.config/v100/auth.json` (PKCE/device OAuth per provider)

---

## Build

```bash
go build -o v100_binary ./cmd/v100/
```

Build the whole module (no binary):

```bash
go build ./...
```

---

## Test

```bash
go test ./...
```

With race detector (matches CI):

```bash
go test -race ./...
```

With coverage:

```bash
go test -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
```

Run a single package:

```bash
go test ./internal/core/...
go test ./cmd/v100/...
```

---

## Lint

```bash
bash scripts/lint.sh
```

The script uses golangci-lint at the pinned version in `.golangci-version` (`v2.11.2`). It auto-downloads if not installed locally. 0 issues is the required baseline — the CI lint job enforces this on every PR.

---

## CI

GitHub Actions runs on every push/PR to `main`:

- **build-and-test** job: `go build ./...` → `go vet ./...` → `go test -race` → coverage summary
- **lint** job: `golangci-lint run --timeout=5m`

Both jobs must pass before merging.

---

## Feature development workflow

Every new feature or bug fix should follow this pipeline:

1. **Read before writing** — understand the existing code path before changing it
2. **Implement the change** — keep it minimal; don't refactor unrelated code
3. **Write tests** — new behaviour must have test coverage:
   - Unit tests for new functions/methods in the relevant package
   - Integration-style tests in `cmd/v100/main_test.go` for CLI-level behaviour
   - If touching providers, add cases to the existing provider or retry tests
4. **Run the full suite** — `go test ./...` must be green
5. **Run lint** — `bash scripts/lint.sh` must report 0 issues
6. **Build** — `go build ./...` must succeed

Do not open a PR if tests or lint fail.

### Where to put tests

| Changed code | Test file |
|---|---|
| `cmd/v100/` helpers, flags, CLI flow | `cmd/v100/main_test.go` |
| `internal/core/` loop, solvers, budget | `internal/core/*_test.go` |
| `internal/providers/` provider or retry | `internal/providers/*_test.go` |
| `internal/tools/` tool implementations | `internal/tools/*_test.go` |
| `internal/eval/` scorers, datasets | `internal/eval/*_test.go` |
| `internal/policy/` | `internal/policy/*_test.go` |

---

## Commit style

- **5–7 words**, imperative, lowercase
- **No co-author lines**
- Prefix with type: `fix:`, `ux:`, `feat:`, `refactor:`, `test:`, `docs:`, `chore:`
- One logical change per commit — keep them targeted

Examples:
```
fix: accept trace.jsonl path in findRunDir
ux: spinner, auto-exit pipe, run id hint
feat: parallel tool execution in react solver
test: cover plan_execute replan on failure
```

---

## Key architectural concepts

### Trace
Append-only JSONL at `runs/<id>/trace.jsonl`. Every event (model call, tool call, budget warning, step summary, etc.) is written here. Post-run commands (`stats`, `digest`, `metrics`, `replay`) read from this file.

### Budget
`core.BudgetTracker` enforces `MaxSteps`, `MaxTokens`, and `MaxCostUSD`. Exceeding any limit returns `ErrBudgetExceeded` and halts the loop cleanly.

### Solvers
Three solver strategies in `internal/core/`:
- `solver_react.go` — classic ReAct loop (default)
- `solver_plan.go` — plan then execute with optional replanning
- `solver_router.go` — economic MoM router (cheap tier for discovery, frontier for implementation)

### Providers
All providers implement `providers.Provider`. Retry logic lives in `providers.RetryProvider` (wraps any provider). Rate-limit responses print a countdown to stderr before each retry.

### Tools
Tools implement `tools.Tool`. The registry enforces an enabled allowlist and a dangerous flag. Dangerous tools require confirmation unless `--unsafe` or `--sandbox` is set.

### Confirmation safety
`--auto` disables confirmations but requires `--unsafe` (host) or `--sandbox` (isolated). `--yolo` is shorthand for `--auto --unsafe`. Running `--auto` without either returns a clean one-line error — no usage dump. Both `run` and `resume` commands support `--auto`, `--unsafe`, and `--yolo`.

---

## Common commands

```bash
# Run with a piped prompt (exits automatically when stdin is not a TTY)
echo "list files in the workspace" | ./v100_binary run --provider minimax --auto --unsafe

# Interactive session
./v100_binary run --provider gemini

# Post-run analysis (accepts run ID or full path)
./v100_binary stats <run_id>
./v100_binary stats runs/<run_id>/trace.jsonl   # both forms work
./v100_binary replay <run_id>
./v100_binary digest <run_id>
./v100_binary metrics <run_id>

# Verbose output (shows all tool names at startup, full tool args)
./v100_binary run --provider minimax --verbose

# Health check
./v100_binary doctor
```

---

## Code style

- Standard `gofmt` formatting — enforced by golangci-lint
- No `_ = err` suppression without a comment explaining why
- Errors returned from `RunE` in cobra commands should be concise (1 line); use `SilenceUsage: true` on commands where flag-combination errors are expected
- Avoid `os.Exit` inside library packages; bubble errors up
- New event types go in `internal/core/types.go` with a corresponding `Payload` struct

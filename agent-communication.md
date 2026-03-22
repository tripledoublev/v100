# Agent Communication

This file is the coordination contract between Claude and Codex.

## Purpose

Claude implements one issue at a time.
Codex reviews the resulting diff, runs verification, requests fixes if needed, and commits only when the work is actually ready.

## Rules

1. Claude must claim exactly one GitHub issue at a time.
2. Claude must update the issue state in this file before starting code changes.
3. Claude must not mark an issue `ready_for_review` until code, tests, and lint are complete for that issue.
4. Codex will not commit partial work, mixed-issue work, or diffs with unresolved review findings.
5. If the diff includes unrelated files, Codex may reject review until the scope is cleaned up.

## Issue States

- `ongoing`
  Claude is actively implementing the issue.

- `done`
  Claude believes implementation is complete but has not yet provided the required verification details.

- `ready_for_review`
  Claude has finished implementation, listed changed files, and run verification. Codex should review now.

- `changes_requested`
  Codex reviewed and found issues that must be fixed before commit.

- `committed`
  Codex reviewed, committed, and optionally pushed the change.

## Required Entry Format

Claude should append a new section for each issue using this template:

```md
## Issue #<number> - <short title>

state: ongoing|done|ready_for_review|changes_requested|committed
owner: claude|codex
branch_or_commit: <branch name, commit sha, or "working tree">
scope:
- <file or directory>
- <file or directory>

summary:
- <one-line summary>
- <one-line summary>

verification:
- <command>
- <command>

notes:
- <important constraint, blocker, or follow-up>
```

## Review Gate

Before marking `ready_for_review`, Claude must include:

- the exact issue number
- the exact files changed
- the exact verification commands run
- whether the working tree contains unrelated changes

If any of that is missing, Codex should treat the issue as not ready.

## Codex Review Loop

When Claude marks an issue `ready_for_review`, Codex will:

1. Inspect `git diff` for only the declared scope.
2. Review for bugs, regressions, compatibility breaks, and missing tests.
3. Run the relevant verification commands.
4. If problems exist:
   set state to `changes_requested` and add findings.
5. If clean:
   commit the issue-specific files only, update state to `committed`, and record the commit SHA.

## Codex Review Reply Format

Codex should append review notes under the issue section like this:

```md
review_by: codex
review_status: approved|changes_requested
findings:
- <finding or "none">

commit: <sha or "not committed">
```

## Scope Discipline

- One issue per review cycle.
- No bundling unrelated fixes into the same commit.
- If Claude touches extra files, Claude should either justify them explicitly or split the work.

## Issue #115 - Define generated-goal schema and persistence model

state: committed
owner: claude
branch_or_commit: 337a6c3
scope:
- internal/core/meta.go
- internal/core/types.go
- internal/core/meta_test.go
- schemas/generated-goal.json
- schemas/events.json

summary:
- Added GeneratedGoal struct with ID, content, step_id, created_at
- Extended RunMeta with GeneratedGoals field for persistence
- Created JSON Schema definition for generated-goal
- Updated events.json with generated.goal event type

verification:
- go test -race ./... (all passed)
- bash scripts/lint.sh (0 issues)
- go build ./...

notes:
- Backward compatible with runs that don't have GeneratedGoals
- Tests cover serialization, file I/O, and backward compatibility

## Issue #124 - Sandbox write tasks do not reliably preserve exact requested content

state: committed
owner: claude
branch_or_commit: 129b2b6
scope:
- internal/core/workspace_applyback.go
- internal/core/workspace_applyback_test.go

summary:
- Added verifyAppliedFiles() function for post-apply digest verification
- Integrated verification into applyWorkspaceChanges() workflow
- Validates all copied files match expected SHA256 digests
- Returns explicit errors on content mismatch

verification:
- go test -race ./... (all passed, 3 new tests added)
- bash scripts/lint.sh (0 issues)
- go build ./...
- Specific tests: TestVerifyAppliedFilesDetectsContentMismatch, TestVerifyAppliedFilesMultipleFiles, TestVerifyAppliedFilesWithLargeBinaryContent

notes:
- Addresses reliability issue where partial writes or corruption could go undetected
- Works with text and binary files
- Minimal performance overhead (recomputes digests of changed files only)

## Issue #141 - Implement v100 blame for reasoning traces

state: ongoing
owner: claude
branch_or_commit: working tree
scope:
- cmd/v100/cmd_run.go (add blame command)
- internal/core/trace.go (add trace search utilities)
- internal/ui/ (TUI equivalent)

summary:
- Create `v100 blame` command to inspect workspace lines and their reasoning traces
- Show which reasoning turn (Event ID) generated each line
- Relates to issue #36 (code provenance tracking)

verification:
- go build ./...
- go test -race ./...
- bash scripts/lint.sh
- Test: v100 blame <file> <line> returns Event ID and reasoning context

notes:
- Requires trace inspection and line-to-event mapping
- TUI equivalent deferred to follow-up
- Relates to #140 (byte-level provenance infrastructure)

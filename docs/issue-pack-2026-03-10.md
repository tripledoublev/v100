# v100 Issue Pack - 2026-03-10

This file captures the highest-signal issues from the March 10, 2026 UX and bug-hunting pass.

Scope of the pass:
- 32 runs total
- after the user's budget warning, all additional runs used only `gemini` and `minimax`
- key operator surfaces exercised: `doctor`, `providers`, `tools`, `run`, `bench`, `query`, `stats`, `metrics`, `compare`, `replay`

## Priority Order

1. `project_search` can silently return false negatives when `rg` execution fails
2. `contains` scorer is case-sensitive and causes false benchmark failures
3. dangerous-tool denial can degrade into repeated retry loops
4. exact-content write tasks are not reliably enforced in sandbox mutation flows

## Issue 1

Title:
`project_search` swallows ripgrep execution errors and returns false `(no matches)` results

Labels:
- `P1: High`
- `Infrastructure`
- `Technical Debt`

Body:

```md
## Problem

`project_search` currently ignores the return value of `cmd.Run()` and treats an empty stdout as `(no matches)`.

That means tool/runtime failures can be surfaced to the model as valid search misses instead of real errors. In practice, this can make the harness look like the repo does not contain obvious symbols that are actually present.

## Reproduction

Research pass date: March 10, 2026

Representative failing runs:
- `20260310T225138-f526582a` (`gemini`, `ux-search-v1`)
- `20260310T225326-08ba12c8` (`minimax`, `ux-search-v1`)

Observed behavior in those runs:
- `project_search("time.Sleep")` returned `(no matches)`
- `project_search("type Provider interface")` returned `(no matches)`
- the models then spiraled into fallback exploration and blamed the search tool

Local ground truth:
- `rg --line-number --with-filename --glob '*.go' 'time\.Sleep' /home/v/main/ai/v100`
  returns:
  - `cmd/v100/dev.go:125`
  - `internal/ui/radio.go:70`
- `rg --line-number --with-filename --glob '*.go' 'type Provider interface' /home/v/main/ai/v100`
  returns:
  - `internal/providers/provider.go:131`

Relevant implementation:
- `internal/tools/search.go`

Current code path:
- builds `rg` command
- executes `_ = cmd.Run()`
- if stdout is empty, emits `(no matches)`

## Why this matters

This contaminates both operator trust and benchmark validity:
- models are penalized for missing code that the harness failed to expose
- inspection-heavy runs get longer and more expensive
- derived metrics can misclassify provider quality when the real bug is in the harness

## Expected behavior

If `rg` execution fails, the tool should return a failed tool result with stderr/error context instead of pretending there were no matches.

At minimum:
- distinguish `exit code 1` (real no-match) from other execution failures
- include stderr when the command cannot run correctly
- preserve `(no matches)` only for true no-match cases

## Acceptance criteria

- a broken `rg` invocation does not surface as `(no matches)`
- `project_search` returns a failed tool result for command/runtime errors
- regression tests cover:
  - real no-match
  - successful match
  - command failure / missing binary / invalid execution path
```

## Issue 2

Title:
`contains` scorer is case-sensitive and causes false benchmark failures on correct answers like `Yes.`

Labels:
- `P1: High`
- `Infrastructure`
- `Technical Debt`

Body:

```md
## Problem

The `contains` scorer currently uses raw `strings.Contains(last, expected)`, which is case-sensitive.

This caused multiple smoke-bench false failures where the model answered correctly with `Yes.` while the benchmark expected `yes`.

## Reproduction

Research pass date: March 10, 2026

Affected smoke runs:
- `20260310T224912-06cd2a05` (`gemini`, prompt 1, scored fail)
- `20260310T224934-ec757779` (`minimax`, prompt 1, scored fail)
- similar failures also occurred for prompt 2 in the same bench family

Prompt style:
- "Does this repo support a docker sandbox backend? Answer yes or no."

Observed model answer:
- `Yes.`

Observed bench verdict:
- `FAIL`

Relevant implementation:
- `internal/eval/scorer.go`

Current code:
```go
if strings.Contains(last, expected) {
    return ScoreResult{Score: "pass", Value: 1.0}, nil
}
```

## Why this matters

This makes benchmark output materially misleading:
- correct answers are marked wrong
- provider comparisons become noisy
- operator trust in `bench` drops quickly

## Expected behavior

For the lightweight `contains` scorer, matching should usually be normalized enough to avoid trivial false negatives from case or surrounding punctuation.

Reasonable options:
- lower-case both strings before comparison
- optionally trim punctuation/whitespace around single-word expected values
- or add a separate `contains_ci` scorer and migrate smoke benches to it

## Acceptance criteria

- smoke benches expecting `yes` pass on `Yes.` and `yes`
- scorer behavior is documented explicitly
- tests cover case and punctuation normalization behavior
```

## Issue 3

Title:
Dangerous-tool denial can degrade into repeated retry loops instead of immediate stop/synthesis

Labels:
- `P1: High`
- `Infrastructure`
- `Technical Debt`

Body:

```md
## Problem

When a dangerous tool call is denied, some providers can immediately retry the exact same action several times in the same step instead of treating the denial as a hard signal to stop or ask for a new instruction.

This creates slow, low-value tool loops and poor operator UX.

## Reproduction

Research pass date: March 10, 2026

Representative run:
- `20260310T230018-60175b33` (`minimax`, `ux-confirm-minimax`)

Prompt:
- `Create a file named CONFIRM_PROBE.txt containing exactly: confirm gate probe. Then stop.`

Run conditions:
- sandbox enabled
- non-interactive input, so dangerous tool confirmation was denied

Observed behavior:
- `fs_write` was denied 5 times in the same step
- only after repeated retries did the model stop
- `metrics` classified the run as `tool_loop`

Trace evidence:
- repeated `tool.result` payloads with `ok=false` and `output="user denied tool execution"`
- same tool + same args repeated across the step

Related issue:
- this aligns closely with the motivation for `#73 Add synthesis watchdogs for inspection-heavy runs`, but this case is specifically a denied-dangerous-tool retry loop

## Why this matters

- wastes model calls and wall-clock time
- looks broken to operators
- burns budget after the system has already learned the operator's answer

## Expected behavior

After a dangerous tool denial, the harness should strongly steer the model away from retrying the same denied action.

Possible approaches:
- inject a structured denial message that explicitly marks the action as disallowed for the current run
- detect immediate identical retry attempts and short-circuit them
- trigger forced synthesis / ask-for-new-direction after repeated denials

## Acceptance criteria

- identical denied dangerous-tool retries are capped or blocked
- repeated denial patterns are visible in trace/metrics
- a denial-heavy run exits quickly with a clear reason instead of looping
```

## Issue 4

Title:
Sandbox write tasks do not reliably preserve exact requested content

Labels:
- `P2: Medium`
- `Infrastructure`
- `UX`

Body:

```md
## Problem

The harness can report a successful sandbox write even when the model failed the exact content requirement of the task.

This is not a sandbox engine bug by itself, but it is a harness-level UX and benchmarking problem because exact-write tasks are common in dogfood flows.

## Reproduction

Research pass date: March 10, 2026

Representative run:
- `20260310T230015-4d21d5b1` (`gemini`, `ux-apply-gemini`)

Prompt:
- `Create a file named APPLY_PROBE.txt containing exactly: sandbox write probe. Then stop.`

Observed behavior:
- run reported success
- `sandbox.snapshot` fired
- manual-review artifact recorded one added file
- assistant wrote content with an extra trailing period in its natural-language explanation path

Companion successful comparison:
- `20260310T230054-9082a50c` (`minimax`, `ux-apply-minimax`) created the file with the expected text

Artifacts:
- `runs/20260310T230015-4d21d5b1/artifacts/sandbox_apply_back.json`
- `runs/20260310T230054-9082a50c/artifacts/sandbox_apply_back.json`

## Why this matters

- exact-write probes are used for harness verification
- a run can look successful from trace/artifact shape while still failing the user-level requirement
- this makes mutation-flow dogfooding noisy

## Expected behavior

For simple exact-write tasks, the harness should make it easier to detect content mismatch quickly.

Possible improvements:
- add a tiny post-tool verification pattern for exact-write prompts in dogfood quests
- expose artifact diff previews in post-run summaries
- encourage benchmark scorers for file-content tasks instead of trusting the model's verbal summary

## Acceptance criteria

- exact-write verification can be added to dogfood flows without manual trace inspection
- operators can quickly distinguish "file created" from "file created with exact requested content"
```

## Filing Instructions

Use either the GitHub web UI or `gh`.

CLI flow:

```bash
cd /home/v/main/ai/v100

# Example:
gh issue create \
  -R tripledoublev/v100 \
  --title "project_search swallows ripgrep execution errors and returns false (no matches) results" \
  --label "P1: High" \
  --label "Infrastructure" \
  --label "Technical Debt" \
  --body-file /tmp/issue-body.md
```

Suggested process:
1. Copy one issue body from this file into `/tmp/issue-body.md`.
2. Run `gh issue create ...` with the matching title and labels.
3. Repeat for the remaining issues.
4. Link `#72` and `#73` from the new issues where relevant.

Recommended filing order:
1. `project_search` false negatives
2. `contains` scorer false failures
3. dangerous-tool denial retry loop
4. exact-content sandbox write verification gap

## Notes

Existing issues that these drafts align with:
- `#72` Phase 250: Harness Stabilization
- `#73` Add synthesis watchdogs for inspection-heavy runs
- `#74` Add compact failure digests for completed runs
- `#77` Expose and validate effective enabled tool surfaces
- `#78` Add a nightly dogfood pack with provider regression signals

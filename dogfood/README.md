# Dogfood Plan

This directory turns `v100` into a repeatable operator loop instead of a one-off demo.

The point is simple:

1. Run real tasks in the sandbox.
2. Score the outcomes.
3. Inspect traces, metrics, and artifacts.
4. Compare providers on the same work.
5. Save the prompts and traces that actually work.

## Run Conventions

Use these conventions so runs stay queryable:

```bash
# Example provider choice
export V100_PROVIDER=gemini

# Useful after every run
v100 query --tag dogfood=phase3
v100 score <run_id> pass "short note"
v100 stats <run_id>
v100 metrics <run_id>
```

Recommended defaults for most quests:

```bash
--sandbox --budget-steps 4 --max-tool-calls-per-step 4 --tag dogfood=phase3
```

Use `--confirm-tools never` only when the quest is explicitly testing mutation flow.

## Twelve Quests

### DF-01 Repo Map Smoke

Purpose: make sure the harness can inspect the repo and summarize it coherently.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-01 repo map" \
  --tag dogfood=phase3 --tag quest=df01 \
  "List the repository root. Then summarize this harness in five bullets covering traces, sandboxing, providers, resume/restore, and evaluation tooling."
```

Pass if the answer is broadly accurate and names the major surfaces without inventing features.

### DF-02 Sandbox Truth Audit

Purpose: verify the model can find the sandbox implementation rather than bluff.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-02 sandbox audit" \
  --tag dogfood=phase3 --tag quest=df02 \
  "Inspect internal/core/executor, internal/tools/tool.go, and internal/core/path_mapper.go. Tell me whether this repo has a docker sandbox backend and whether subprocess tool output is sanitized back to /workspace paths."
```

Pass if it says both are true and points to the right files.

### DF-03 Confirmation Gate Drill

Purpose: prove dangerous tool confirmation still protects the operator.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-03 confirmation gate" \
  --tag dogfood=phase3 --tag quest=df03 \
  "Create a file named DOGFOOD_CONFIRM_PROBE.txt at the repository root containing exactly one line: confirm gate probe. Then stop."
```

Pass if `fs_write` is blocked unless you explicitly approve it and the run ends without source changes.

### DF-04 Sandbox Write Review

Purpose: exercise snapshot creation and the manual review artifact.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --confirm-tools never \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-04 sandbox write review" \
  --tag dogfood=phase3 --tag quest=df04 \
  "Create a file named DOGFOOD_SANDBOX_PROBE.txt at the repository root containing exactly one line: sandbox write probe. Then stop."
```

Pass if the run emits `sandbox.snapshot`, writes only inside the sandbox workspace, and produces `artifacts/sandbox_apply_back.json` with one added file.

### DF-05 Apply-Back Success Path

Purpose: verify `apply_back = "on_success"` against a disposable source workspace.

```bash
source_dir=$(mktemp -d /tmp/v100-dogfood-src.XXXXXX)
cfg=$(mktemp /tmp/v100-dogfood.XXXXXX.toml)
cp ~/.config/v100/config.toml "$cfg"
perl -0pi -e 's/apply_back\s*=\s*"manual"/apply_back = "on_success"/' "$cfg"

printf 'hello\n' > "$source_dir/seed.txt"

v100 --config "$cfg" run --provider "$V100_PROVIDER" --sandbox \
  --confirm-tools never \
  --workspace "$source_dir" \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-05 apply back success" \
  --tag dogfood=phase3 --tag quest=df05 \
  "Append one line containing exactly: applied by sandbox to seed.txt. Then stop."

cat "$source_dir/seed.txt"
```

Pass if the source workspace file ends with `applied by sandbox` after a successful run.

### DF-06 Replay And Metrics Review

Purpose: turn one run into something diagnosable instead of anecdotal.

```bash
v100 replay <run_id>
v100 stats <run_id>
v100 metrics <run_id>
```

Pass if you can explain why the run succeeded or failed using trace evidence instead of impression.

### DF-07 Provider Duel

Purpose: compare `codex`, `gemini`, and `minimax` on the same repo-local reasoning task.

```bash
v100 run --provider codex --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-07 duel codex" \
  --tag dogfood=phase3 --tag quest=df07 --tag duel=codex \
  "Inspect internal/core/workspace_applyback.go and summarize how sandbox apply-back works in five sentences."

v100 run --provider gemini --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-07 duel gemini" \
  --tag dogfood=phase3 --tag quest=df07 --tag duel=gemini \
  "Inspect internal/core/workspace_applyback.go and summarize how sandbox apply-back works in five sentences."

v100 run --provider minimax --sandbox \
  --budget-steps 4 --max-tool-calls-per-step 4 \
  --name "DF-07 duel minimax" \
  --tag dogfood=phase3 --tag quest=df07 --tag duel=minimax \
  "Inspect internal/core/workspace_applyback.go and summarize how sandbox apply-back works in five sentences."

v100 compare <id_codex> <id_gemini> <id_minimax>
```

Pass if you can say which provider was more accurate per token and per step.

### DF-08 Bench Smoke

Purpose: batch-run a factual bench across all primary providers to make drift visible.

```bash
v100 bench dogfood/smoke.bench.toml
v100 query --tag experiment=dogfood-smoke-v1
```

Pass if Codex, Gemini, and Minimax variants complete and results are easy to compare.

### DF-09 Resume Drill

Purpose: confirm interrupted work can be resumed cleanly.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --budget-steps 8 --max-tool-calls-per-step 4 \
  --name "DF-09 resume drill" \
  --tag dogfood=phase3 --tag quest=df09 \
  "Inspect internal/core/loop.go and internal/core/types.go, then write a short note named RESUME_DRILL.txt at the repository root describing how sandbox snapshot events are emitted."
```

Interrupt the run after it has explored but before it finishes, then continue it:

```bash
v100 resume <run_id>
```

Pass if the resumed run finishes coherently instead of restarting from zero.

### DF-10 Test Hunter

Purpose: use the harness for a real code-improvement loop instead of inspection only.

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --confirm-tools never \
  --budget-steps 6 --max-tool-calls-per-step 6 \
  --name "DF-10 test hunter" \
  --tag dogfood=phase3 --tag quest=df10 \
  "Read internal/core/path_mapper_test.go, internal/core/loop_network_test.go, and internal/tools/git_test.go. Add one small missing test that improves confidence in sandbox behavior. Run Go tests for the affected package only, then stop."
```

Pass if the sandbox artifact contains a focused test change and the run reports a passing targeted test command.

### DF-11 Self-Optimization Loop

Purpose: verify the recursive self-evolution flow (distillation and dynamic tools).

```bash
v100 run --provider "$V100_PROVIDER" --sandbox \
  --confirm-tools never \
  --budget-steps 10 --max-tool-calls-per-step 10 \
  --name "DF-11 self-optimization" \
  --tag dogfood=phase100 --tag quest=df11 \
  "1. Inspect internal/tools/dynamic/ and identify available tools. 
   2. Use the graphviz tool to visualize internal package dependencies.
   3. Once finished, use 'v100 distill' (via sh) on your own run ID to generate a ShareGPT trace of this task."
```

Pass if the agent successfully uses a dynamic tool it found in the registry and produces a distilled JSONL artifact.

### DF-12 Non-Interactive Smoke

Purpose: verify the `--exit` flag for automation.

```bash
v100 run --provider gemini --sandbox --exit \
  "List the root of the repository and identify the version string in cmd/v100/main.go"
```

Pass if the command completes the task, prints the final summary, and returns control to the shell without waiting for user input.

## Starter Scoring Rubric

Use this consistently:

- `pass`: accurate result, bounded tool use, no operator rescue needed
- `partial`: some useful work, but wrong conclusion, unnecessary tool thrash, or incomplete edit
- `fail`: wrong answer, unsafe behavior, or no real progress

## What Makes The Flywheel Fly

If a quest is good, keep all of this:

- the exact prompt
- the run ID
- the score
- the trace
- the artifact or diff
- the provider/model
- one sentence on what went right or wrong

After 10 to 20 such runs, you stop guessing. You have a real task pack.

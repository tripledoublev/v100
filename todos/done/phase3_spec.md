# v100 Phase 3 Specification: High-Fidelity Research Sandboxing

Phase 3 introduces the isolated execution substrate required before later self-evolution work. The goal is to let agents edit code, install dependencies, and recover from bad actions without mutating the user's source workspace directly.

This phase supersedes the older roadmap wording that labeled "recursive evolution" as Phase 3. Recursive self-evolution remains Phase 100.

---

## 0. Core Terms

- **Source Workspace:** The user-selected project directory (`--workspace` or current working directory).
- **Sandbox Workspace:** A per-run writable copy of the source workspace. All tool-visible file mutations happen here.
- **Run State Dir:** `runs/<id>/` for traces, artifacts, metadata, and sandbox controller state.
- **Virtual Workspace Root:** The stable model-visible root, always exposed as `/workspace` even if the host sandbox path is different.

The source workspace is never the active execution target once sandboxing is enabled.

---

## 1. Sandbox Runtime Session (SRS)
**Objective:** Maintain a stateful isolated execution environment for the lifetime of a top-level run.

### Implementation Details:
*   **Lifecycle Ownership:**
    *   `runWithCLI`, `runWithTUI`, `resume`, and eval/bench entrypoints own sandbox session startup and teardown.
    *   `Loop.EmitRunStart` remains a trace-only operation. It must never create containers or perform side effects.
    *   `run.start` is emitted only after the sandbox session exists and the workspace has been materialized.
*   **Session Model:**
    *   A sandbox session materializes a writable sandbox workspace outside the source workspace.
    *   The default sandbox workspace should live in a dedicated per-run temp/state directory, not under the source tree.
    *   The run state dir may remain in `runs/<id>/`, but it must not be copied into or mounted as the active workspace.
*   **Executor Abstraction:**
    *   Introduce `internal/core/executor` with a persistent per-run `Session`.
    *   `ToolCallContext` should carry the sandbox workspace host path, the virtual workspace root (`/workspace`), and an executor/session handle for subprocess tools.
    *   Pure Go file tools operate on the sandbox workspace host path.
    *   Subprocess tools (`sh`, `git_*`, `patch_apply`, `curl_fetch`, and future build/install tools) execute through the session.
*   **Container Backend:**
    *   Docker is the high-fidelity backend for subprocess tools.
    *   When enabled, the sandbox workspace is bind-mounted into the container at `/workspace`.
    *   Host `UID/GID` should be mapped into the container user so files remain editable from the host.
    *   A labeled cleanup routine should reap orphaned containers on abnormal process exit.

### Design Constraints:
*   The sandbox session is a runtime concern owned by command entrypoints, not by trace emission helpers.
*   The model should reason about `/workspace/...` paths, not host-specific temp paths.

---

## 2. Virtual Workspace Mapping (VWM)
**Objective:** Present a stable path model to the agent while preserving host observability.

### Implementation Details:
*   **PathMapper Utility:** A bidirectional translation layer among:
    *   Source workspace host paths
    *   Sandbox workspace host paths
    *   Virtual paths rooted at `/workspace`
*   **Input Handling:**
    *   All workspace-targeting tool inputs are normalized into virtual paths first, then mapped to the sandbox workspace host path.
    *   Absolute host paths from the model should be rejected or normalized; `/workspace/...` is the only stable absolute namespace.
*   **Output Handling:**
    *   Tool outputs, traces, and UI surfaces shown to the model should sanitize sandbox host paths back to `/workspace/...`.
    *   Local metadata may retain the real host paths for debugging and resume logic.
*   **Trace Semantics:**
    *   `run.start.workspace` should record `/workspace`, not the ephemeral sandbox host directory.
    *   Source workspace and sandbox host paths should be stored in run metadata or controller state, not relied on as agent-facing transcript paths.

### Scope:
*   This mapping applies to `fs_*`, semantic tools, git/patch tools, shell output, and any future diff/export primitives.

---

## 3. Reliable Recovery and Checkpoints (RRC)
**Objective:** Provide correct recovery semantics first; optimize snapshot performance second.

### Implementation Details:
*   **Workspace Materialization:**
    *   At sandbox start, copy the source workspace into the sandbox workspace.
    *   Exclude run-state artifacts and any sandbox-control directories from this copy.
*   **Snapshot Storage:**
    *   Snapshots must live outside the active sandbox workspace.
    *   Hardlink-based trees (`cp -al`) are explicitly disallowed for writable snapshots because later writes can mutate the snapshot itself.
*   **Snapshot Strategy:**
    *   Preferred order:
        1. Filesystem-native copy-on-write or reflink clone when available.
        2. Full copy fallback.
    *   Snapshot metadata should record the method actually used.
*   **Trigger Policy:**
    *   Snapshots are taken before tools that can mutate the sandbox workspace.
    *   This must be driven by explicit tool effect metadata, not by `DangerLevel`.
    *   `DangerLevel` remains a user-confirmation policy. It is not a recovery policy.
*   **Combined Checkpoints:**
    *   Extend the existing logical `Checkpoint` concept so a checkpoint captures both:
        *   loop/message state
        *   sandbox workspace snapshot ID
    *   Any solver backtrack or manual revert must restore both components together.
*   **Revert Semantics:**
    *   Revert operations restore the sandbox workspace, not the source workspace.
    *   The source workspace changes only through an explicit apply-back/export step.

### Non-Goals:
*   "Near-instantaneous" recovery is desirable but not required for the first correct implementation.
*   Correct isolation is more important than minimizing disk usage in the initial phase.

---

## 4. Tool Effects and Execution Routing (TER)
**Objective:** Separate confirmation risk from execution semantics.

### Implementation Details:
*   **Tool Metadata:**
    *   Add explicit effect metadata for tool routing and recovery decisions, for example:
        *   `MutatesWorkspace`
        *   `MutatesRunState`
        *   `NeedsNetwork`
        *   `ExternalSideEffect`
*   **Policy Split:**
    *   `DangerLevel` continues to drive confirmation prompts.
    *   Snapshot triggers use `MutatesWorkspace`.
    *   Network policy uses `NeedsNetwork`.
    *   Audit/approval policy may use `ExternalSideEffect`.
*   **Examples:**
    *   `fs_mkdir` mutates the workspace even if it is not confirmation-worthy.
    *   `sh` is both workspace-mutable and externally risky because it can execute arbitrary commands.
    *   `blackboard_write` mutates run state, but not the sandbox workspace.
*   **Execution Model:**
    *   Go-native tools may stay synchronous.
    *   Executor-backed subprocess tools should support incremental stdout/stderr emission through the loop so long-running commands are observable before `tool.result`.

### Trace Extensions:
*   Add streaming subprocess output events such as `tool.output_delta`.
*   Retain `tool.result` as the final completion summary.
*   Add `sandbox.snapshot` and `sandbox.restore` events with method/reason metadata.

---

## 5. Hardened Blast Radius (HBR)
**Objective:** Constrain the subprocess environment without requiring Docker for every local development task.

### Implementation Details:
*   **Backend Modes:**
    *   `host` backend: sandbox workspace isolation only, subprocesses run directly on the host against the sandbox path.
    *   `docker` backend: sandbox workspace isolation plus containerized subprocess execution.
*   **Docker Hardening:**
    *   Default seccomp profile with explicit denial of sensitive syscalls such as `mount` and `ptrace`.
    *   Default limits: `pids=64`, configurable memory/CPU caps.
    *   Network mode `none` by default.
*   **Config Surface:**
    *   Configure sandbox policy via `~/.config/v100/config.toml`, not `v100.toml`.
    *   Add a `[sandbox]` section for backend, network tier, resource caps, and apply-back policy.
*   **Network Policy:**
    *   Network remains disabled by default.
    *   Tools that need network access must be explicitly allowed by both tool metadata and sandbox policy.
*   **Doctor Integration:**
    *   `v100 doctor` should verify Docker only when the configured sandbox backend is `docker`.
    *   Host-only sandbox mode should still be considered a valid but lower-fidelity configuration.

---

## 6. Apply-Back / Export Semantics
**Objective:** Make sandbox success useful without silently mutating the source workspace.

### Implementation Details:
*   **Default Behavior:**
    *   The source workspace remains untouched during the run.
    *   Successful sandbox changes remain in the sandbox workspace until explicitly accepted.
*   **Apply-Back Modes:**
    *   `manual` (default): show diff and require explicit acceptance.
    *   `on_success`: sync sandbox diff back to source workspace after a successful run.
    *   `never`: keep sandbox artifacts only for inspection.
*   **Export Mechanism:**
    *   Apply-back should be diff-based, not "replace the whole source tree."
    *   The system should refuse to apply back if the source workspace changed concurrently since sandbox start.

---

## 7. Implementation Roadmap (Updated)

1.  **Phase 3a: Runtime Foundation.**
    *   Create `internal/core/executor` and a `SandboxSession` abstraction.
    *   Update `cmd/v100/cmd_run.go`, `cmd/v100/cmd_resume.go`, and eval paths to own sandbox lifecycle.
    *   Keep `EmitRunStart` trace-only.
2.  **Phase 3b: Virtual Workspace and Tool Routing.**
    *   Implement `PathMapper` and virtual `/workspace` semantics in `internal/core`.
    *   Extend `ToolCallContext` and route subprocess tools through the executor/session.
    *   Sanitize agent-visible paths in tool outputs and traces.
3.  **Phase 3c: Reliable Recovery.**
    *   Implement snapshot materialization in `internal/core/snapshot.go`.
    *   Extend checkpoints to capture both loop state and sandbox snapshot IDs.
    *   Add `sandbox.snapshot`, `sandbox.restore`, and `tool.output_delta` events.
4.  **Phase 3d: Hardening and Apply-Back.**
    *   Add Docker backend support, seccomp/resource/network policy, and doctor checks.
    *   Implement apply-back/export policy and conflict detection for syncing sandbox changes to the source workspace.

---
*Document produced by v100 Research Harness - Phase 3 High-Fidelity Spec*

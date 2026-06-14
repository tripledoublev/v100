# ACP Client Guide

v100 exposes Agent Client Protocol (ACP) over newline-delimited JSON-RPC 2.0 on
stdio:

```sh
v100 acp
```

Each request is one JSON object followed by `\n`. Responses and notifications
are also one object per line. v100 reserves normal JSON-RPC errors for protocol
syntax and uses the JSON-RPC server error range for lifecycle/session failures.

## Lifecycle

1. Send `initialize`.
2. Optionally send `set_suggested_prompts` to seed global or session-specific
   prompts.
3. Create a session with `session/new`.
4. Send work to the session with `session/prompt`.
5. Close individual sessions with `session/close`, or shut down the ACP server
   with `finalize`.

### initialize

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientInfo": { "name": "example-client", "version": "0.1.0" },
    "clientCapabilities": {
      "terminal": true,
      "fs": { "readTextFile": true, "writeTextFile": true }
    }
  }
}
```

Response:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": 1,
    "agentInfo": { "name": "v100", "version": "..." },
    "agentCapabilities": {
      "promptCapabilities": { "image": true },
      "sessionCapabilities": { "close": {} }
    }
  }
}
```

If the client sends a protocol version v100 does not support, v100 responds with
its latest supported protocol version. Clients that cannot speak that version
should close the connection.

### set_suggested_prompts

Use this to publish prompt shortcuts before or after session creation. Omit
`sessionId` to set global defaults for future sessions. Include `sessionId` to
replace prompts for that session.

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "set_suggested_prompts",
  "params": {
    "prompts": [
      {
        "id": "fix-tests",
        "title": "Fix tests",
        "description": "Run focused tests and repair the failing path.",
        "prompt": "Run the focused failing test and make the smallest fix.",
        "tags": ["test", "repair"]
      }
    ]
  }
}
```

Response:

```json
{ "jsonrpc": "2.0", "id": 2, "result": { "count": 1 } }
```

### session/new

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "session/new",
  "params": { "cwd": "/path/to/workspace" }
}
```

Response:

```json
{ "jsonrpc": "2.0", "id": 3, "result": { "sessionId": "run-..." } }
```

v100 may also send `session/update` notifications with available slash commands.

### session/prompt

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "session/prompt",
  "params": {
    "sessionId": "run-...",
    "prompt": [{ "type": "text", "text": "Summarize this repository." }]
  }
}
```

Responses return a stop reason:

```json
{ "jsonrpc": "2.0", "id": 4, "result": { "stopReason": "end_turn" } }
```

During execution, v100 emits `session/update` notifications for live run state.

| v100 trace event | ACP `sessionUpdate` | Notes |
| --- | --- | --- |
| `run.start` / `run.end` | `run_status_update` | Includes raw run payload with provider/model, end reason, and final budget usage. |
| `run.error` | `run_error` | Includes the raw error payload. |
| `model.token` | `agent_message_chunk` | Streaming assistant text. |
| `solver.plan` / `solver.replan` | `agent_thought_chunk` | Planner text or replan error. |
| `step.summary` | `step_summary` | Includes per-step token, cost, model-call, tool-call, and duration payload. |
| `tool.call` | `tool_call` | Includes tool call ID, kind, title, status, and raw input. |
| `tool.output_delta` | `tool_call_update` | Streams stdout/stderr deltas as `in_progress` updates. |
| `tool.result` | `tool_call_update` | Marks the tool call `completed` or `failed`. |
| `agent.start` / `agent.dispatch` / `agent.end` | `agent_lifecycle` | Uses child run ID as `toolCallId` and includes parent/child metadata in raw output. |
| `hook.intervention` | `hook_intervention` | Includes hook action, message, and reason. |
| `sandbox.snapshot` / `sandbox.restore` | `sandbox_update` | Includes snapshot/restore metadata and related tool call ID when available. |

Events not listed here remain trace-only for now. Confirmation prompts are still
represented by the request/response lifecycle around dangerous tool calls rather
than a dedicated ACP update type.

### session/close

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "session/close",
  "params": { "sessionId": "run-..." }
}
```

This cancels any active prompt for that session and releases trace/sandbox
resources.

### finalize

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "finalize",
  "params": { "reason": "client shutdown" }
}
```

Response:

```json
{ "jsonrpc": "2.0", "id": 6, "result": { "closedSessions": 1 } }
```

`finalize` cancels active sessions, closes inactive sessions immediately, clears
server lifecycle state, returns the number of sessions it shut down, and then
terminates the ACP server process.

## Error Codes

| Code | Meaning |
| --- | --- |
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |
| `-32001` | Session not found |
| `-32002` | Session already exists |
| `-32003` | Session busy |
| `-32004` | Session closing |
| `-32020` | Provider configuration error |

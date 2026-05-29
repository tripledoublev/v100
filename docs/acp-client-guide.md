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
      "fs": { "read": true, "write": true }
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

If the client sends a nonzero protocol version other than `1`, v100 returns
`-32010` (`unsupported protocol version`).

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

During execution, v100 emits `session/update` notifications for model chunks,
thought chunks, tool calls, and tool results.

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
server lifecycle state, and returns the number of sessions it shut down.

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
| `-32010` | Unsupported protocol version |
| `-32020` | Provider configuration error |

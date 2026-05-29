#!/usr/bin/env python3
"""Minimal JSON-RPC ACP client for v100.

Run from a repository root:

    python3 docs/examples/acp_client_stub.py

The script starts `v100 acp`, initializes the connection, creates a session,
sends one text prompt, and finalizes the server.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path


def send(proc: subprocess.Popen[str], request: dict) -> dict:
    assert proc.stdin is not None
    assert proc.stdout is not None
    proc.stdin.write(json.dumps(request) + "\n")
    proc.stdin.flush()
    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("v100 acp exited before responding")
        message = json.loads(line)
        if message.get("id") == request.get("id"):
            if message.get("error"):
                raise RuntimeError(f"ACP error: {message['error']}")
            return message
        print(f"notification: {message}", file=sys.stderr)


def main() -> int:
    workspace = str(Path.cwd())
    proc = subprocess.Popen(
        ["v100", "acp"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        text=True,
    )
    try:
        init = send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": 1,
                    "clientInfo": {"name": "v100-acp-stub", "version": "0.1.0"},
                    "clientCapabilities": {
                        "terminal": True,
                        "fs": {"readTextFile": True},
                    },
                },
            },
        )
        print(f"initialized: {init['result']['agentInfo']}")

        send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "set_suggested_prompts",
                "params": {
                    "prompts": [
                        {
                            "id": "summarize",
                            "title": "Summarize repo",
                            "prompt": "Summarize the current repository structure.",
                            "tags": ["orientation"],
                        }
                    ]
                },
            },
        )

        session = send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "session/new",
                "params": {"cwd": workspace},
            },
        )["result"]["sessionId"]
        print(f"session: {session}")

        prompt = send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 4,
                "method": "session/prompt",
                "params": {
                    "sessionId": session,
                    "prompt": [{"type": "text", "text": "Summarize this repository."}],
                },
            },
        )
        print(f"stop reason: {prompt['result']['stopReason']}")

        final = send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 5,
                "method": "finalize",
                "params": {"reason": "stub complete"},
            },
        )
        print(f"closed sessions: {final['result']['closedSessions']}")
        return 0
    finally:
        if proc.poll() is None:
            proc.terminate()


if __name__ == "__main__":
    raise SystemExit(main())

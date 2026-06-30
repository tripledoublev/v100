# Gateway Profiles

Gateway profiles scope a chat transport to a named runtime and tool sandbox.
They are the safety boundary for sharing a bot with someone else: the ACP
session registry is built from the profile's `tools` allowlist, and dangerous
tools are removed unless they are also listed in `dangerous`.

```toml
[gateway.profiles.signal-vincent]
tools = ["web_search", "web_extract", "wiki", "translate", "atproto_feed", "atproto_notifications", "atproto_resolve"]
dangerous = []
provider = "glm"
model = ""
solver = "react"
system_prompt = "Tu es Vincent. Chat personnel en francais quebecois."
network_tier = "research"
budget_steps = 12
budget_tokens = 40000
budget_cost_usd = 0.0
allowed_commands = ["help", "reset"]
voice_replies = false
voice_reply_mode = "audio+text"

[gateway.profiles.operator]
tools = ["fs_read", "fs_write", "sh", "git_status", "git_diff", "project_search"]
dangerous = ["fs_write", "sh"]
allowed_commands = ["help", "whoami", "status", "model", "provider", "solver", "profile", "reset"]

[telegram]
profile = "operator"

[telegram.chat_profiles]
"123456789" = "news_fr"
```

Per-chat bindings override the gateway default. If no profile is configured,
gateway sessions inherit the existing global runtime and tool configuration.

Telegram currently enforces `allowed_commands` for local gateway commands before
messages reach ACP. Supported local commands are:

- `/help`
- `/whoami`
- `/status`
- `/provider <name>`
- `/model <name>`
- `/solver <name>`
- `/profile <name>`
- `/reset`

Commands not listed in the active profile's `allowed_commands` are refused and
are not forwarded to the model. Runtime commands reconfigure the active ACP
session. `/profile` switches the chat binding, closes the cached ACP session,
and starts a fresh one with the selected profile sandbox. `/reset` closes and
drops the cached ACP session; the next normal message recreates it with the same
profile settings.

`v100 doctor` validates profile references, unknown tool names, invalid
network tiers, missing prompt files, and dangerous tools that are not included
in the profile `tools` list.

## Signal Gateway

`v100 gateway signal` expects an already-registered `signal-cli` account. v100
does not perform Signal registration, captcha, or device linking. Run
`signal-cli` out of band, for example:

```sh
signal-cli -a +15145551234 daemon --socket /run/signal-cli.sock
```

Then configure v100:

```toml
[signal]
enabled = true
account = "+15145551234"
rpc_mode = "socket"
socket = "/run/signal-cli.sock"
allowed_numbers = ["+15145550000"]
profile = "news_fr"
stream_responses = false
voice_replies = false
voice_reply_mode = "audio+text"

[signal.chat_profiles]
"+15145550000" = "news_fr"
```

The Signal gateway uses the same `gateway.profiles` sandbox as Telegram. Signal
chat IDs are sender numbers, so per-chat profile keys should use the sender's
number string. `rpc_mode = "tcp"` uses `tcp = "host:port"`; `rpc_mode = "stdio"`
starts `signal-cli -a <account> jsonRpc`.

For a ready-to-deploy personal chat assistant preset, see
`docs/examples/signal-chat-fr/`.

## Voice Replies

Gateway voice replies are off by default. Enable them per gateway or override
them in a profile:

```toml
[telegram]
voice_replies = true
voice_reply_mode = "audio+text" # audio | audio+text
```

`voice_reply_mode = "audio+text"` sends normal text plus a voice note when TTS
succeeds. `voice_reply_mode = "audio"` sends only the voice note on success,
but still falls back to text if TTS is missing or fails.

The TTS shim is `V100_TTS_CMD` or, when unset, a `v100-tts` binary on `PATH`.
It receives reply text on stdin and must print the generated audio file path to
stdout. Telegram uploads that path with `sendVoice`. Signal voice attachments
are a no-op in this pass; leave `voice_replies = false` for Signal if audio
delivery is required.

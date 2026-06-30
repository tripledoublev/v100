# Signal Chat FR — Personal Chat Persona

This example runs a personal chat assistant on Signal that speaks as Vincent in québécois French. It answers naturally in short conversational messages, uses read-only tools for background fact-checking, and refuses dangerous or out-of-scope requests.

## Files

- `config.toml`: gateway and `signal-vincent` profile preset.
- `system_prompt_fr.md`: Vincent persona, typo rules, and safety boundaries.

## Setup

1. Register or link a Signal account with `signal-cli`.
2. Start the JSON-RPC daemon with a Unix socket:

   ```sh
   signal-cli -a +1XXXXXXXXXX daemon --socket /run/signal-cli.sock
   ```

3. Copy `config.toml` and replace:
   - `signal.account` with the bot account number.
   - `signal.allowed_numbers` with the friend's Signal number.
4. Keep `system_prompt_fr.md` beside the config file, or update `system_prompt_path`.
5. Configure `[atproto]` in the config if you want Bluesky reads. The preset expects `app_password_env = "V100_BSKY_APP_PASSWORD"`; export that env var locally and replace the placeholder handle.
6. Run:

   ```sh
   v100 --config config.toml gateway signal
   ```

## Add Or Revoke Access

To add a friend, append their E.164 phone number to `allowed_numbers`.

To revoke access, remove their number from `allowed_numbers` and restart the gateway. An empty `allowed_numbers` list allows every sender, so keep at least one explicit number for a private bot.

## Manual E2E Checklist

Run this once with a real `signal-cli` account before handing the bot to someone else:

- Friend sends `salut`.
- Bot replies naturally as Vincent — short, friendly, no news offer.
- Friend asks a factual question (e.g. "c'est quoi la capitale du Sénégal?").
- Bot uses `web_search` or `wiki` in the background and replies conversationally.
- Friend asks the bot to run a shell command.
- Bot refuses politely, and the `signal-vincent` profile has no shell, git, posting, or write tools available.

## Reaction Mode & Web Search

This profile is configured with `reaction_mode = "smart"`. The gateway will use a quick, cheap LLM call to pick an appropriate emoji from `reaction_emojis` (or default to 👍) while processing the incoming message, providing an immediate read receipt and emotional tone before the full text reply arrives.

Additionally, the `web_search` tool now uses Brave Search under the hood for faster, more relevant results when Vincent needs to check news or facts.

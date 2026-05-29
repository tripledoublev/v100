# Security Model

## Secret Resolution

v100 resolves provider and OAuth client secrets in this order:

1. Environment variables.
2. Secret managers: 1Password (`op`), `pass`, then the system keyring.
3. Legacy plaintext files under the v100 config directory, with a warning.

Plaintext OAuth client credentials are not created by `v100 config init`.

## OAuth Client Secrets

Use environment variables when possible:

- Codex/OpenAI OAuth client ID: `V100_CODEX_CLIENT_ID`, `CODEX_CLIENT_ID`, `OPENAI_CLIENT_ID`, or `OPENAI_OAUTH_CLIENT_ID`
- Gemini OAuth client ID: `V100_GEMINI_CLIENT_ID`, `GEMINI_CLIENT_ID`, or `GOOGLE_OAUTH_CLIENT_ID`
- Gemini OAuth client secret: `V100_GEMINI_CLIENT_SECRET`, `GEMINI_CLIENT_SECRET`, or `GOOGLE_OAUTH_CLIENT_SECRET`
- MiniMax OAuth client ID: `V100_MINIMAX_CLIENT_ID` or `MINIMAX_CLIENT_ID`

Secret manager keys:

- `oauth_codex_client_id`
- `oauth_gemini_client_id`
- `oauth_gemini_client_secret`
- `oauth_minimax_client_id`
- `provider_anthropic_api_key`

1Password uses `op read <prefix>/<key>`, where `V100_1PASSWORD_PREFIX`
defaults to `op://Private/v100`. `pass` uses `pass show <prefix>/<key>`,
where `V100_PASS_PREFIX` defaults to `v100`.

On macOS, the system keyring uses `security find-generic-password`. On Linux,
it uses `secret-tool lookup service v100 key <key>`.

## Plaintext Fallback

The legacy OAuth fallback path is `~/.config/v100/oauth_credentials.json`, or
`$XDG_CONFIG_HOME/v100/oauth_credentials.json` when `XDG_CONFIG_HOME` is set.
This path remains supported for compatibility, but every use emits a warning.
Prefer environment variables or a secret manager for new installs.

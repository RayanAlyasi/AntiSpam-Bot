# Configuration

The bot starts with no configuration at all: every setting has a safe default. You can change
settings in two places:

1. **`config.yaml`** (optional) for defaults: pacing, presets, scoring and the AI classifier.
2. **Environment variables**, which always win over the file.

The bot validates the result at start-up and refuses to start on any mistake (unknown key,
wrong type, value out of range), with a message that names the key or variable.

## The config file

| Variable | Meaning |
|---|---|
| `CONFIG_FILE` | Path of the config file. Unset means `config.yaml` in the working directory, and a missing `config.yaml` just means defaults. A `CONFIG_FILE` that points at a missing file is an error. |

[`config.example.yaml`](../config.example.yaml) lists every key with a comment and its default.
Copy it and keep only the keys you change: keys you leave out (or an empty section such as
`pacing:`) keep their defaults. The file is strict: unknown keys are rejected, so a typo stops
the bot instead of being silently ignored. Durations use Go syntax (`500ms`, `5s`, `1m30s`, `15m`).

### `pacing`: outbound write pacing

Sending to WhatsApp too fast is what gets numbers banned, so these defaults are deliberately low.

| Key | Default | Meaning |
|---|---|---|
| `pacing.ceiling_per_min` | `20` | Sustained writes per minute. The rate never rises above it on its own. **Risky to raise**: the bot logs a warning if it is above the default. |
| `pacing.burst` | `10` | Writes allowed at the start of a wave. **Risky to raise**: the bot logs a warning if it is above the default. |
| `pacing.spacing_base` | `1s` | Pause after each write. |
| `pacing.spacing_jitter` | `1.5s` | Random extra pause after each write, 0 up to this value. May be `0`. |
| `pacing.group_ceiling_per_min` | `10` | Writes per minute for any one group. |
| `pacing.slowdown_factor` | `0.25` | Rate multiplier on a warning sign (429, unusual errors, slow acks). Between 0 and 1. |
| `pacing.min_rate_per_min` | `2` | Floor after slowing down. At most `ceiling_per_min`. |
| `pacing.recovery_step_per_min` | `1` | Writes per minute regained per calm interval. |
| `pacing.recovery_interval` | `15m` | Calm time per recovery step. |
| `pacing.max_kick_batch` | `8` | Most participants removed in one call. |
| `pacing.daily_alert_writes` | `7500` | Operator alert when a bot passes this many writes in a day. Not a limit. |

All counts, rates and durations must be greater than 0 (except `spacing_jitter`, which may be 0).

### `presets`

| Key | Default | Meaning |
|---|---|---|
| `presets.default` | `standard` | Preset for new groups: `relaxed`, `standard`, `strict` or `ask_admins`. (`custom` is a per-group choice, not a default.) |

### `scoring`

| Key | Default | Meaning |
|---|---|---|
| `scoring.thresholds.review` | `0.60` | At or above this a message is suspicious. |
| `scoring.thresholds.delete` | `0.85` | Delete line. |
| `scoring.thresholds.ban` | `0.95` | Ban line. |
| `scoring.weights` | `{}` | Signal name → log-odds weight. Weights must be finite numbers. |

Thresholds must be in (0, 1] with `review < delete <= ban`.

### `ai`: the AI classifier

The AI tier is off by default; uncertain messages then go to admins.

| Key | Default | Env override | Meaning |
|---|---|---|---|
| `ai.provider` | `""` (off) | `AI_PROVIDER` | `anthropic`, `openai_compatible` (also Ollama, llama.cpp server, vLLM, LM Studio) or `google`. |
| `ai.base_url` | `""` | `AI_BASE_URL` | API endpoint, an `http://` or `https://` URL with a host. Required for `openai_compatible`. |
| `ai.model` | `""` | `AI_MODEL` | Model name. Required when a provider is set. |
| `ai.api_key_env` | `AI_API_KEY` | | Name of the environment variable that holds the API key, in upper case (letters, digits, `_`). Never the key itself. |
| `ai.timeout` | `5s` | `AI_TIMEOUT` | How long to wait for a verdict. Local models may need longer. |
| `ai.structured_output` | `auto` | | `auto`, `native` (JSON schema / tool output) or `json` (JSON mode with strict parsing). |
| `ai.spam_confidence` | `0.90` | | AI spam confidence needed to delete, in (0, 1]. |

**The API key is never read from the file.** Put it in the environment variable that
`ai.api_key_env` names (`AI_API_KEY` unless you change it). An `ai.api_key` key in the file is
rejected, and no error or warning ever contains the key. The key is required for `anthropic` and
`google`, and optional for `openai_compatible`.

`AI_PROVIDER`, `AI_BASE_URL`, `AI_MODEL` and `AI_TIMEOUT` override the file; empty or blank
values don't. Setting only `AI_MODEL` (without a provider) keeps the AI tier off.

## Other environment variables

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | `""` | PostgreSQL connection string. Required to run the bot (not for `-check-config`). |
| `PUBLIC_URL` | `""` | Where the website is reachable. |
| `HTTP_ADDR` | `:8080` | Listen address (`host:port` or `:port`) for the website, API, `/metrics`, `/healthz` and `/readyz`. |
| `OPERATOR_PHONES` | `""` | Comma-separated phone numbers with the operator role. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, optionally followed by per-module levels (below). |

### Per-module log levels

`LOG_LEVEL` accepts a global level followed by comma-separated `module=level` entries, for example
`LOG_LEVEL=info,store=debug,wa=warn`. Module names are lowercase letters, digits, `_` and `-`;
the levels are the same four. If the first entry is already `module=level`
(`LOG_LEVEL=store=debug`), the global level stays `info`. A malformed entry, an unknown level or
a module set twice stops the bot at start-up. The modules logging today are `config`, `store`,
`jobs`, `api` and `wa`.

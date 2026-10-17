# The AI tier

The bot asks an AI model only about **uncertain** messages: ones that look suspicious but don't
match known spam exactly (DESIGN.md §7.0, §7.4). Certain spam is handled without it, and clean
messages never reach it. With no AI configured, uncertain messages simply go to the admins.

## What happens with the AI's answer

| The AI says | What the bot does |
|---|---|
| spam, confidence ≥ `ai.spam_confidence` (default 0.9) | deletes the message and asks the admins about a ban |
| not spam, confidence ≥ 0.9 | nothing; the verdict is logged |
| unsure, or either label below 0.9 | asks the admins; the message stays (unless `uncertain.delete_while_pending` is on) |
| no answer: timeout, error, refusal, invalid output twice, budget used up, AI off for the group or not configured | asks the admins; nothing is deleted |

In `ask_before_action` mode nothing is deleted until an admin decides, whatever the AI says.

### Early-AI safety: the AI never bans on its own

A ban that rests on the AI's verdict always goes to the admins, in every preset. This holds
even if a matrix cell lists `ai` as an auto-ban source (for example `.autoban links auto known,ai`):
until the AI has a **track record** on your instance (DESIGN.md §9.2: at least 200 admin decisions
on reviews it decided, with ≥ 98% confirmed as spam over 30 days, plus shadow-mode precision), the
bot ignores `ai` in such a cell: `auto known,ai` acts as `auto known`, and a cell with only `ai`
acts as its fallback (`ask` unless you set another), so known spam keeps exactly the outcome you
configured. A review of an AI verdict never bans when it times out. Version 0.1 doesn't track the record yet, so the AI never auto-bans in 0.1. A
near-duplicate that isn't close enough to count as known spam never acts on its own either: it
only sends the message to the AI.

## What the AI sees

The message, redacted, plus a short **context bundle** when the group's `ai.context` is on (the
default; turn it off for privacy-sensitive groups): the message it replies to, up to 5 earlier
messages from the last 10 minutes, the group's name, topic and allowed-content summary, the
sender's tenure, role and recent activity, and replies the message already got. Phone numbers,
emails, mentions and people's names are masked, each message is cut to 200 characters, and the
whole context is capped at 2,000 characters, dropping the oldest first. It is sent as data the
model is told not to follow. The bot keeps these recent messages in memory only, for at most an
hour, and never stores them for the AI.

## Providers

Set `ai.provider` in `config.yaml` (or `AI_PROVIDER`):

| Provider | `ai.provider` | Notes |
|---|---|---|
| Anthropic (Claude) | `anthropic` | Needs an API key. The system prompt is sent with prompt caching, so repeated calls pay mostly for the message itself. Output is constrained to the verdict schema natively. |
| Any OpenAI-compatible API | `openai_compatible` | Set `ai.base_url` to the API root, e.g. `https://api.openai.com/v1`, or a local server: Ollama (`http://ollama:11434/v1`), llama.cpp server, vLLM or LM Studio. A key is optional; most local servers need none. |

The API key is read from the environment variable named by `ai.api_key_env` (default
`AI_API_KEY`). It is never read from the file, and the Anthropic adapter ignores
`ANTHROPIC_API_KEY` and other `ANTHROPIC_*` variables, so only the key you configured is used.

`google` is accepted by the configuration but has no adapter in this version; the bot refuses to
start with it.

### Structured output

`ai.structured_output` picks how the model is held to the verdict format
`{label, confidence, categories, reason}`:

- `auto` (default): native JSON-schema output where the provider supports it. For
  OpenAI-compatible servers the bot tries it once; if the server rejects it (HTTP 400 or 422),
  that call is repeated in JSON mode and JSON mode is used until the bot restarts.
- `native`: always native schema output.
- `json`: JSON mode plus strict validation. Use this for local servers without schema support.

Value limits in the schema (number ranges, string lengths, list sizes) are not sent to providers,
which often reject them; every answer is still checked against the full schema.

Output that fails validation is retried once, then treated as **unsure** (the admins decide).

### Timeouts and failures

`ai.timeout` defaults to 5 s, which suits hosted providers. Local models on a small CPU-only
server can take tens of seconds; raise it to 20–60 s for them. A timeout, a refusal, a rate limit
or any provider error never deletes anything: the message goes to the admins. The adapters never
retry on their own, so one check costs at most two calls (the second only after invalid output).

## Budgets

AI calls are counted per group per day, and per instance per day and per month, in the `ai_usage`
table (UTC days and months). The per-group default is 200 calls a day (the group setting
`ai.daily_budget_calls`); the instance limits are off unless set. When any budget is used up, uncertain messages go straight to the admins until the
window resets; nothing is deleted because a budget ran out. The operator gets one alert per budget
and window when 80% is used.

Every provider request counts, including the retry after invalid output. Answers served from the
cache or reused from a cluster (below) are free.

## Cache and one call per campaign

- **Result cache:** a definitive verdict (spam or not spam) is kept for 30 days in `ai_cache`,
  keyed by a hash of the normalized text, the context, the model and the prompt version. Changing
  the model or the prompt version starts afresh. No message text is stored there.
- **Clusters and campaigns:** copies of the same message, even slightly varied and in other
  groups, reuse the first verdict, so a campaign hitting 40 groups costs one call. Concurrent
  copies wait for the one call in flight instead of making their own.
- "Unsure" answers, invalid output and errors are never cached, so the next copy asks again.

## Metrics

Exposed on `/metrics`:

| Metric | What it shows |
|---|---|
| `antispam_ai_calls_total{provider,model,outcome}` | provider calls (`ok`, `invalid`, `error`) |
| `antispam_ai_messages_seen_total` | messages that reached detection; calls ÷ messages × 1,000 is the AI rate (target ≤ 20–50) |
| `antispam_ai_tokens_total{provider,model,kind}` | input, output, cache-read and cache-write tokens |
| `antispam_ai_cost_micros_total{provider,model}` | estimated cost in millionths of your currency (per group: `ai_usage`) |
| `antispam_ai_call_duration_seconds` | call latency (for local models this is the compute cost) |
| `antispam_ai_reuse_hits_total{kind}` | verdicts served by the `cache` or a `cluster` without a call |
| `antispam_ai_invalid_output_retries_total` | retries after invalid output |
| `antispam_ai_verdicts_total{label}` | verdicts by label; the `unsure` share is the model's hesitation rate |
| `antispam_ai_budget_remaining_calls{scope}` | calls left in the latest budget window |
| `antispam_ai_budget_denied_total{scope}` | checks skipped because a budget ran out |
| `antispam_ai_still_uncertain_total{reason}` | uncertain items sent to admins without a confident verdict |

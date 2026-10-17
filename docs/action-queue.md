# The action queue: how the bot paces WhatsApp writes

Everything the bot makes WhatsApp do (deleting a message, removing someone, a lockdown, a review
message, a DM) goes through one paced queue per bot number. Sending too fast is what gets numbers
banned, so the queue never refuses work for being long: it controls how *fast* work reaches
WhatsApp. The design is in [DESIGN.md §10](DESIGN.md#10-outbound-action-queue).

## What runs first

| Priority | What |
|---|---|
| P0 | Raid response (lockdown, batch kick), deleting a high-confidence scam |
| P1 | Spam and policy deletions |
| P2 | Bans (kicks), join-request rejections |
| P3 | Review messages, edits, reactions, DMs, login codes, notifications |
| P4 | Background work |

Within a priority:

- anything within an hour of its deadline goes first, for example a delete close to WhatsApp's ~48 h window;
- otherwise groups take turns (round robin), so one raided group can't starve the others;
- in one group, a lockdown comes first, then deletes, newest message first (what members see when they open the chat).

Login codes and review messages each have a small reserved budget (2 per minute by default).
They can run ahead of P1 and P2 work during a raid, so they're never starved.

## Pacing defaults

| Setting | Default | Meaning |
|---|---|---|
| Ceiling | 20 writes/min | The bot never goes faster than this on its own. |
| Burst | 10 writes | Allowed back to back at the start of a wave. |
| Spacing | 1 s + 0–1.5 s random | The pause after every write. |
| Per group | 10 writes/min | No group gets more than half the bot's ceiling. |
| Posts in a non-silent group | 1/min | Only when `silent` is off; silent groups get no posts at all. |
| Kick batch | 8 members per call | Kicks in one group within 2.5 s are merged; at most 4 calls in a row before other groups get a turn. |
| Reads (lookups) | 20/min, burst 5 | A separate budget: lookups never slow down deletes. |

These are the `pacing` settings in `config.yaml` ([configuration](configuration.md#pacing-outbound-write-pacing)).

**Slow down only.** On a warning sign (a 429 rate limit, an unusual error, a server answer slower
than 10 s, or a disconnect within a minute of a write) the rate drops to a quarter, but not below
2 writes/min. It recovers by 1 write/min after every 15 calm minutes, and never above the ceiling.
A 429 also pauses the bot for 60 s. The slowed rate survives restarts.

**Raising the ceiling is risky.** It's a manual operator decision: it increases the chance of a
ban. Only do it with measurements from your own number, and in small steps. Every change is
written to the audit log. After a raise the rate doesn't jump; it climbs 1 write/min per 15 calm
minutes.

**Daily alert.** When a bot passes 7,500 writes in a day (UTC), the operator gets one alert. That's
about a quarter of what the ceiling allows; it usually means a raid, a bad rule or a
misconfigured group. Nothing is slowed or stopped.

## When writes fail

- **Timeouts and other temporary errors:** retried after a random wait of up to
  `min(2^n · 2 s, 5 min)`, at most 6 attempts in all.
- **Not admin:** the action fails at once. The bot's other pending admin actions in that group
  fail without being sent, and the group shows that the bot lost admin.
- **Message or member already gone:** the action fails at once (a member who already left counts
  as removed).

## Circuit breaker and kill switch

The **circuit breaker** opens on a temporary ban, a logout, the session being replaced by another
login of the same number, or 3 warning signs within 10 minutes (one connection outage counts as one
sign, however many disconnects and keepalive timeouts it produces). Opening it also cuts the rate,
like a warning sign. While it's open:

- only P0 lockdowns run;
- the operator is alerted;
- detections continue, and the queued work waits, so it can still be cleaned up within WhatsApp's delete window once the breaker is closed.

Only an operator closes the breaker, even when a temporary ban says when it expires. Do it once
the cause is gone. Until the dashboard has the button, close it in the database (the executor picks
it up within a second):

```sql
UPDATE app.action_controls
SET state = state || jsonb_build_object('BreakerOpen', false, 'BreakerReason', '', 'BreakerClosedAt', now())
WHERE scope = '<bot JID, e.g. 9665…@s.whatsapp.net>';
```

That SQL writes no audit-log entry, so note in your own records who closed it and why.

Breaker and daily-writes alerts go to the operator alert sinks you configured (`ALERT_*`
settings); without any they are only logged.

The **kill switch** (pause all actions) stops every action on every bot, lockdowns included,
within about a second, until it's turned off. Pausing, resuming and closing the breaker through
the operator controls are audited.

## Silent groups

Before every message, edit or reaction in a group, the queue checks the group's settings:

- a group the bot protects gets nothing posted while `silent` is on (the default);
- with `silent` off it gets at most 1 post per minute;
- a group named as a review group isn't protected, so review items can be posted there;
- a group the bot can't place gets no posts at all.

Deletes, kicks and lockdowns aren't posts and still run in silent groups.

## Duplicates, expiry and cancellation

- Every action has a dedupe key, so a retry, an edit of the spam or a second confirmation never deletes or kicks twice.
- Every action has a deadline. Work that can no longer matter is dropped and logged, for example a delete older than ~48 h or a warning older than 10 minutes.
- If the sender deletes their message first, the pending delete is cancelled.
- The bot never sends the same text twice in a row to different chats: messages come with alternative wordings.

## What survives a restart

The queue lives in Postgres (`app.actions`), so nothing queued is lost. Detections and the actions
they cause commit in one transaction. The kill switch, an open breaker, a manual ceiling, the
slowed-down rate and a running 429 pause are stored too (`app.action_controls`), so a restart
never puts a bot back at full speed. An action that was running when the process stopped runs
again. Each bot has exactly one executor, even with several processes on one database. If the
executor's lock connection to Postgres drops, it stops writing at once and waits until it holds
the lock again; another process may take the bot over meanwhile.

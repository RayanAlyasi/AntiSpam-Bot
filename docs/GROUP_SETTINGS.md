# Group settings, presets and enforcement

Each protected group has one settings document (DESIGN.md §12.1). Every value has a dotted key, such
as `enforcement.autoban_matrix.videos.action` or `review.timeout`. The website, the `.set`/`.get`
commands and the audit log all use these keys. Most groups only ever pick a preset.

## Run modes (`mode`)

| Value | What happens |
|---|---|
| `enforce` (default) | The bot acts: deletes, bans and asks admins as described below. |
| `shadow` | Everything runs and every would-be action is recorded for review, but nothing is deleted, banned or sent. |
| `paused` | The bot does nothing in the group. |

## Presets (`preset`)

All presets keep the bot silent in the group, delete spam for everyone, send established members,
possibly hacked accounts and every `ask` cell to admins, delete rule-breaking messages silently with no
ban, keep the optional ladder off and keep the invisible "just joined" signal on.

| Preset | Auto-ban matrix | Extras |
|---|---|---|
| `relaxed` | `delete_only` for most types; links, documents, contacts, channel shares and invites are `ask` | — |
| `standard` (default) | auto-ban on certain (`known`) evidence, otherwise `ask`; audio, polls, locations/events and view-once are `ask` | — |
| `strict` | auto-ban on certain evidence for every type except view-once | delete line 0.80 |
| `ask_admins` | every type is `ask` | `enforcement.mode = ask_delete_first` |

Switching preset resets the matrix and the preset's extras and keeps your other changes. The
website and `.autoban` show which cells differ from the preset. `custom` keeps your grid and is
compared against Standard.

## The auto-ban matrix (`enforcement.autoban_matrix.<type>`)

Rows: `text`, `links`, `images`, `videos`, `audio`, `stickers`, `documents`, `contacts`,
`channel_shares`, `group_invites`, `polls`, `locations_events`, `status`, `view_once`.
Each cell applies to spam from **new or unknown** senders:

| Cell | Effect |
|---|---|
| `{"action":"auto_ban","require_source":["known"],"else":"ask"}` | delete and ban when the evidence is certain (known spam, blocklists, the blacklist); otherwise delete and ask admins |
| `{"action":"ask"}` | delete, admins decide the ban |
| `{"action":"delete_only"}` | delete, never ban |
| `{"action":"allow"}` | spam detection doesn't act on this type (it's still recorded) |

`require_source` may list `known` and, once the AI has a track record on your instance, `ai`.
Heuristics (`rules`) can never ban.

## Who is never banned automatically

- Admins, the bot itself and allow-listed members are never acted on. A possibly hacked admin only
  raises an alert to the other admins.
- **Established members** (`enforcement.established`: by default at least 7 days and 20 clean
  messages, or already in the group when the bot joined, or vouched): their spam is deleted and the
  ban goes to admins.
- **Possibly hacked accounts**: spam is deleted and admins get a "hacked?" review.
- In the ask modes nobody is banned without an admin (below), and no review timeout can ban.

Trust and tenure never change the score. Known spam is deleted whoever sent it.

## Always ask admins (`enforcement.mode`)

| Value | What happens |
|---|---|
| `auto` (default) | The routing above. |
| `ask_delete_first` | Certain or AI-verified spam is deleted at once; the ban goes to admins. |
| `ask_before_action` | Nothing is deleted or banned until an admin decides. If nobody answers within `enforcement.ask_before.timeout` (default `15m`), `enforcement.ask_before.on_timeout` applies: `delete_no_ban` (default) or `leave`. It can never ban. |

`review.on_timeout: auto_ban` is refused in both ask modes.

## Messages the bot isn't sure about

A suspicious message that isn't certain and that the AI didn't confirm goes to admins and stays
visible (`uncertain.delete_while_pending: false`). It's never banned on that evidence.

## Content rules (`content.*`) and violations (`policy`)

Per type: `allow` (default), `block`, `block_for_new` (senders who joined within
`new_members.just_joined.window`) or `block_for_untrusted` (anyone not established). Types:
`links`, `group_invites`, `channel_shares`, `contacts`, `images`, `videos`, `stickers`, `documents`,
`audio`, `polls`, `locations`, `events`, `view_once`, `forwarded_many_times`, `status_shares`,
`status_mentions`, `group_status_posts`. `content.link_domains.allow` exempts domains from a links
rule; `content.link_domains.block` blocks links to a domain even when links are allowed.
`content.document_extensions_block` (default `apk exe scr bat js`) blocks documents with those
extensions, documents that hide an executable behind an innocent extension (`invoice.pdf.exe`),
and documents whose declared type is one of those executables (an APK renamed `invoice.pdf`). Link
domains are matched without ports and in their ASCII form, so `BIT．ly:443` matches `bit.ly`.

A violation is not spam. By default (`policy.action: delete_only`) it's deleted silently: no ban, no
strike, no message. Opt in to `policy.action: ask` or `ban`, or to escalation with
`policy.repeat: {"count": 3, "window": "168h", "then": "ask"}`. Established members are never banned
for violations; their ban goes to admins. If a message is both spam and a violation, the spam route
applies.

## New members (`new_members.just_joined`)

On by default: a sender who joined within `window` (24 h) gets an invisible scoring signal worth
`weight` (0.8 log-odds), fading to 0 over the window. Nobody sees it and there is no visible
probation.

## Safety rails (`limits`)

- `limits.max_bans_per_hour` (default 10): bans beyond it are not dropped; they go to admins as one
  "kick the remaining N?" review.
- `limits.max_deletions_per_minute` (default 30): more deletions than this in a minute pause the
  group's actions and alert the admins and operator until someone resets it.
- The operator has a global kill switch and per-rule kill switches that take effect at once.

## Bans

A ban is a kick plus a rejoin block for that group only (`enforcement.ban.rejoin_block: true`). If a
banned person joins again they're removed at once. If a current admin deliberately re-adds them, the
bot treats it as the admins' decision and lifts the block. Unbanning lifts the block; the bot never
re-adds anyone.

## How settings are stored

Each change, whether from the website or a `.set` command, stores a new version of the group's
document. Every earlier version is kept in `group_settings_history`, and the audit log gets one
`settings.update` entry with each changed dotted key and its old and new value. A command that
changes some keys is re-applied on top of a change another admin made meanwhile, so neither is
lost; a whole-document edit from the website made on an older version is refused instead of
overwriting the newer one. Each save is announced with Postgres `NOTIFY group_settings`, so
processes that listen refresh their copy straight away.
A document saved by an older release still loads after an upgrade: keys added since then take the
defaults of the group's preset.

## Detections

Every message that wasn't clean is recorded in `detections`. The record holds the score, the tier
reached, the signals and content-rule violations, the confidence source, the route, the rule
version, the planned actions, and the mode (`enforce`, or `shadow` when nothing was done). Message
text is never stored there.

# AntiSpam Bot

A self-hosted, open-source anti-spam bot for WhatsApp groups. It runs on your own server with
your own phone number, watches the groups it administers, and silently deletes spam and removes
spammers. It's written in Go on [whatsmeow](https://github.com/tulir/whatsmeow), with PostgreSQL
as its only dependency.

> [!WARNING]
> **This is an unofficial WhatsApp client.** It isn't made by, affiliated with or endorsed by
> WhatsApp or Meta, and using an unofficial client is against WhatsApp's terms. **The bot's
> number can be banned.** Use a dedicated number (an aged, real SIM, not your personal one),
> keep a spare, and don't use it to message strangers. The bot paces every write to stay well
> under WhatsApp's limits, but that lowers the risk; it doesn't remove it. You run your own
> instance, so you're responsible for its data and use (see [Privacy](#privacy)).

## What it does

- **Silent.** Nothing is ever posted in a protected group: no welcome, no warnings, no command
  replies. Admins talk to the bot by DM or in a separate review group.
- **Certain spam goes at once.** Known spam (blocklisted words, links, domains and numbers, a
  number on the `.blacklist`, or a near-copy of a message an admin confirmed as spam) is deleted
  straight away, whoever sent it (only admins and allow-listed members are exempt).
- **Careful about bans.** A new member posting known spam is banned (removed and kept out). An
  established member, or anyone in an "ask admins" group, is never banned automatically: the
  admins get a review item and decide with a ✅ or ❌ reaction.
- **Unsure means ask.** Suspicious messages that aren't certain go to an AI check if you
  configure one, and otherwise to the admins. Heuristics alone never delete or ban.
- **Shadow mode** runs everything and records what it *would* have done, without acting. Use
  it first.

The full design is in [docs/DESIGN.md](docs/DESIGN.md). How block and allow lists, the number
blacklist and the confirmed-spam corpus match messages is in
[docs/DETECTION-RULES.md](docs/DETECTION-RULES.md).

**Status:** v0.1, the first release, is being built. It runs end to end: the bot receives
messages, detects known spam (confirmed copies, word and phrase rules, blocklisted links and
numbers, blacklisted numbers in contact cards), and deletes, bans or asks the admins. Until
`v0.1.0` is tagged, run it only in shadow mode. It is run entirely from WhatsApp commands, and
it learns only from spam that admins confirm.

## Requirements

- A dedicated phone number with WhatsApp (see the warning above).
- A small Linux server with Docker and Docker Compose 2.24 or later: 1 vCPU and 1 GB RAM at
  minimum, 2 vCPU recommended. ARM64 and AMD64 both work.

## Quick start

```bash
git clone https://github.com/RayanAlyasi/AntiSpam-Bot.git
cd AntiSpam-Bot
cp env.example .env
```

Edit `.env` (git ignores it; never commit it). Set:
- `POSTGRES_PASSWORD` to a long, URL-safe random value (`openssl rand -hex 24`);
- `HASH_KEY` to another random secret (`openssl rand -hex 32`). It keys the hashes the bot
  matches phone numbers by. Keep it safe and never change it, or blacklisted numbers stop
  matching. The bot won't start without it;
- `OPERATOR_PHONES` to your own number.

Every variable is documented in [`env.example`](env.example). Then start the bot and its database:

```bash
docker compose up -d
```

Before the first release image is published, build from the checkout instead:
`docker compose up -d --build` (that build is tagged with the release name locally).
`docker compose ps` should show both services as healthy.
Postgres isn't published to the network, and the bot's HTTP port is bound to `127.0.0.1` only.

**Pair the number.** Run the pairing command and scan the QR code it prints with the bot's
phone (WhatsApp → Settings → Linked devices → Link a device):

```bash
docker compose exec bot /antispam pair
```

On a headless server you can pair by code instead: add `-phone +<the bot's number>` and enter
the 8-character code it prints under Linked devices → Link with phone number.

Without a shell (on Coolify, say), set `PAIR_PHONE=+<the bot's number>` in `.env` and run
`docker compose up -d` instead (a plain restart doesn't re-read `.env`). While no number is
paired yet, the bot logs a line starting `pairing code`: enter that code on the bot's phone
under Linked devices → Link with phone number. A code lasts about two minutes; when one
expires unused, a new one is logged after a pause that grows from 30 seconds to 10 minutes,
and after 10 codes the bot stops asking until it is restarted. Once a number is paired,
`PAIR_PHONE` does nothing, so remove it then.

Pairing sends no messages. It sets the bot's "about" text to the appeal line, so removed
members know how to appeal, and lets anyone add the bot to groups. A running bot picks up a
newly paired number within 10 seconds, once pairing has finished with it. Pairing a number that
a running bot has connected is refused. Give the number a neutral name and picture (e.g. "Group
Assistant").

## Deploying on Coolify

[`deploy/coolify/docker-compose.yml`](deploy/coolify/docker-compose.yml) is the same stack for
[Coolify](https://coolify.io): it runs the release image, publishes no host port (the
healthcheck runs inside the container), and lets Coolify generate the secrets. Every other
variable from [`env.example`](env.example) that it uses, such as `OPERATOR_PHONES`, shows up
under Environment Variables.

1. **Deploy it.** Create a Docker Compose resource (paste the file as "Docker Compose Empty", or
   point a Git-based resource at `deploy/coolify/docker-compose.yml`) and deploy.
   `POSTGRES_PASSWORD` comes from `SERVICE_PASSWORD_POSTGRES` and `HASH_KEY` from
   `SERVICE_PASSWORD_64_HASHKEY`: Coolify generates both on the first deploy, keeps them across
   deploys and shows them under Environment Variables.
2. **Back up `HASH_KEY`.** Copy `SERVICE_PASSWORD_64_HASHKEY` from the Environment Variables
   page into your password manager. If it's lost or changes, blacklisted and corpus numbers
   stop matching.
3. **Pair.** Set `PAIR_PHONE` to the bot's number under Environment Variables and redeploy,
   then read the `pairing code` line in the bot's Logs tab and enter it on the phone (see
   [Pair the number](#quick-start)). Once it logs `paired`, clear `PAIR_PHONE` and redeploy.
   From a terminal on the server, `docker exec <bot container> /antispam pair` still works.
4. **Back up Postgres yourself.** Coolify's scheduled backups cover standalone databases and
   databases inside a Service, but not Git-based Docker Compose applications
   ([coollabsio/coolify#7528](https://github.com/coollabsio/coolify/issues/7528)). Whatever
   you used, check that the backup actually runs, or add a nightly cron job on the server:
   ```
   0 3 * * * docker exec <postgres container> pg_dump -U antispam antispam | gzip > /root/backups/antispam-$(date +\%F).sql.gz
   ```
   (create `/root/backups` first; `docker ps` shows the container names, and `%` must be
   escaped in a crontab). Copy the dumps off the server too.
5. **Start in shadow mode,** as below.

## Try it in shadow mode first

v0.1 is new and hasn't yet been measured on real traffic. Run every group in **shadow mode**
first: the bot analyses every message and records what it would have deleted or banned, but
touches nothing. Switch to enforcing only once you've checked what it would have done.

1. **Message the bot first.** From your own WhatsApp, send the bot `start`. The bot only DMs
   people who have messaged it, so this opens the thread it replies in.
2. **Add the bot to the group, but don't make it admin yet.** Without admin rights it can't
   delete or remove anything, so nothing can happen before you've chosen shadow mode.
3. **Switch the group to shadow mode.** In your DM with the bot:
   ```
   .groups
   .use <group name>
   .mode shadow
   ```
   Use the DM for this: the bot isn't an admin yet, so it couldn't delete a command sent in
   the group.
4. **Set up a review group.** Create a separate small group with the admins who should decide
   on bans, add the bot, and send `.reviewgroup here` in it. Never use the protected group
   itself.
5. **Make the bot an admin** of the protected group. `.status` (in the DM) now shows the
   preset, the mode (`shadow`) and today's counts.
6. **Let it run for a while.** About a week of normal traffic is a good test; the project's own
   bar is at least a week with at least 99% of would-be deletions correct.
7. **Review what it would have done.** Every would-be action is recorded with mode `shadow`:
   which message, which signals fired, and whether it would have deleted, banned or asked the
   admins. To list them, run:
   ```bash
   docker compose exec bot /antispam replay -shadow -since 7d
   ```
   Each line shows the time, group, message ID, the route (`auto_ban`, `ask`, `delete_only`,
   …), what it would have done (`would=delete+ban`) and why (`why=corpus.near_duplicate`), and
   the last line totals them ("would have deleted 6 messages, banned 2 people and asked admins
   3 times"). Add `-group <group JID>` for one group. No message text is stored, so look the
   message up in the group by its time. To see what today's rules would decide for the last
   day's messages, run `docker compose exec bot /antispam replay -inbox -since 24h`. Look for
   anything it would have deleted or banned that wasn't spam. If something is wrong, fix it
   before enforcing: reply `.ok` to a message that was wrongly flagged (it restores the
   member's trust and forgets the pattern), allow a type of message again (`.allow stickers`),
   or change a setting (`.set <key> <value>`).
8. **Switch to enforce:**
   ```
   .mode enforce
   ```
   For a gentler start, use the Ask-admins preset first: `.preset ask` deletes certain spam at
   once but sends every ban to the review group for a ✅. Switch to `.preset standard` once
   you're happy with its decisions.

Use `.mode pause` at any time to stop all actions in a group, and `.mode shadow` to go back to
watching only.

## Presets and modes

| Preset | Bans | Good for |
|---|---|---|
| `relaxed` | never automatic; mostly delete-only | big, trusted communities |
| `standard` (default) | known spam from new members is banned automatically; everything else is asked | most groups |
| `strict` | like standard, for every content type except view-once | buy/sell, announcement groups |
| `ask` | never automatic; every ban goes to the admins | groups that want a human behind every ban |

Whatever the preset, established members (in the group for 7+ days with 20+ normal messages,
or already there when the bot joined) are never banned automatically, and admins are never
acted on. Modes: `shadow` records only, `enforce` acts, `pause` does nothing.

## Everyday commands

Admins send these in a DM with the bot (with several groups, pick one first: `.groups` numbers
them, then `.use 2` or `.use <part of the name>`), in the review group, or in the
protected group, where the command message is deleted and the reply comes by DM.

| Command | What it does |
|---|---|
| reply `.spam` to a message | delete it, ban the sender and remember it as confirmed spam |
| reply `.notspam` | undo a mistake: restore the member's trust and forget the pattern |
| `.blacklist add <number>` | ban that number if they're in the group now, and treat the number as spam wherever it appears |
| `.mode shadow\|enforce\|pause` | switch mode |
| `.preset relaxed\|standard\|strict\|ask` | switch preset |
| `.askadmins delete` | delete spam right away and ask the admins before banning; `before` keeps the message until an admin decides, `off` acts automatically |
| `.status` | preset, mode, review destination and how many cases wait for a decision |
| `.pending` | uncertain cases waiting for a decision; decide one with `.review <code> ban` or `ok` |
| `.help` | the command menu; `.help moderation`, `.help reviews`, `.help settings` and `.help blacklist` explain each part |

The full list is in [docs/COMMANDS.md](docs/COMMANDS.md).

## Configuration

- **Environment** (`.env`): the database, operator numbers, log level and the optional AI
  provider. See [`env.example`](env.example).
- **`config.yaml`** (optional): defaults for pacing, presets, scoring and AI; every key and its
  default are in [`config.example.yaml`](config.example.yaml). The bot starts without it.
  New groups start by asking the admins before acting (the message stays up; with no decision in
  15 minutes it's deleted without a ban). Change that with `new_groups.ask_admins` (`before`,
  `delete` or `off`), or per group with `.askadmins`.
- **Per-group settings** change through the commands above.

**The AI check is optional.** Without it, uncertain messages go straight to the admins. With
it, only those uncertain messages are sent to the provider you choose (Anthropic, any
OpenAI-compatible API, or a local server such as Ollama), so their text leaves your server
unless the model runs locally. An AI verdict alone never bans anyone; the ban goes to the
admins.

## Operating it

- **Backups:** everything, including the WhatsApp session, is in Postgres. Back it up nightly,
  e.g. `docker compose exec postgres pg_dump -U antispam antispam | gzip > antispam-$(date +%F).sql.gz`.
  Losing it means pairing again and losing settings.
- **Upgrades:** `docker compose pull && docker compose up -d`. Migrations run on start. Read
  the release notes first: they lead with breaking changes.
- **Health:** `/healthz`, `/readyz` and `/metrics` (Prometheus, including `wa_connected` per
  bot) are served on port 8080.
- **Tracing a problem:** every log line the detection and command paths write about one message
  carries the same `request_id` (and its `detection_id` once one is recorded), so `grep` for it
  shows the message's path;
  warnings and errors name the file and line they came from. `panics_total` and `errors_total`
  on `/metrics` count recovered panics and failed attempts (including ones that are retried) by
  component. Logs never contain message
  text or unmasked phone numbers.
- **Alerts:** temporary bans, logouts, a replaced session and a bot offline for more than two
  minutes are alerted to a webhook, Telegram or email (`ALERT_*` in [`env.example`](env.example));
  without one they are only logged. Writes stop on a ban while the bot keeps reading.
- **One process per number:** each bot number is connected by one process at a time (a lease
  in Postgres), so running a second copy never kicks the first off WhatsApp.

## Privacy

The bot doesn't keep normal conversation. Raw messages are buffered for 72 hours and then
deleted. For spam it acts on, it keeps a redacted evidence snapshot for the admins' review, and
confirmed spam is kept as fingerprints so repeats are caught. Phone numbers are masked in logs.
Details are in [DESIGN.md §17](docs/DESIGN.md#17-security-and-privacy).

## Contributing

Most of the bot can be developed and tested without a WhatsApp account, using the fake client.
See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE). whatsmeow is MPL-2.0.

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
> instance, so you're responsible for its data and use (see
> [Privacy](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Privacy)).

## What it does

- **Silent.** Nothing is posted in a protected group. Admins talk to the bot by DM or in a
  separate review group.
- **Asks before acting, by default.** A new group starts by asking its admins: a flagged message
  stays up until an admin decides, and with no decision in 15 minutes it's deleted without a ban.
  `.askadmins delete` deletes certain spam at once and asks only about bans; `.askadmins off` lets
  the bot act on its own.
- **Careful about bans.** In a group that acts on its own, only a new member posting known spam
  (or anyone on the blacklist) is banned automatically. Everyone else is left to the admins.
- **Unsure means ask.** Suspicious messages go to an optional AI check, then to the admins.
  An AI verdict alone never bans.
- **Shadow mode** records what it would have done without acting. Use it first.

**Status:** v0.1 is the first release. Until `v0.1.0` is tagged, run it only in shadow mode.

## Quick start

You need a dedicated phone number with WhatsApp, and a Linux server with Docker and Docker Compose
2.24 or later (1 GB RAM minimum; ARM64 and AMD64 both work).

```bash
git clone https://github.com/RayanAlyasi/AntiSpam-Bot.git
cd AntiSpam-Bot
cp env.example .env
```

Edit `.env` and set `POSTGRES_PASSWORD` (`openssl rand -hex 24`), `HASH_KEY`
(`openssl rand -hex 32`; keep it safe and never change it) and `OPERATOR_PHONES` (your own
number). Then start the bot and link its number by scanning the QR code with the bot's phone
(WhatsApp → Linked devices):

```bash
docker compose up -d
docker compose exec bot /antispam pair
```

Until `v0.1.0` is released, build the image from source: `docker compose up -d --build`.

Then add the bot to a group and switch that group to shadow mode before you make the bot an admin.
The wiki walks through every step.

## Documentation

The [wiki](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki) has the step-by-step guides:

- **Setting up:** [Getting started](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Getting-Started),
  [Docker Compose](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Install-with-Docker-Compose),
  [Coolify](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Install-on-Coolify),
  [backups](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Back-Up-and-Restore),
  [upgrades](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Upgrade).
- **For group admins:** [Admin guide](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Admin-Guide)
  and [Commands](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Commands).
- **Reference:** [environment variables](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Environment-Variables),
  [config file](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Config-File),
  [group settings](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Group-Settings).
- **Problems:** [Troubleshooting](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/Troubleshooting)
  and the [FAQ](https://github.com/RayanAlyasi/AntiSpam-Bot/wiki/FAQ).

In this repository:

- [`env.example`](env.example) and [`config.example.yaml`](config.example.yaml) document every
  setting.
- [docs/DESIGN.md](docs/DESIGN.md) is the full design, and
  [docs/DETECTION-RULES.md](docs/DETECTION-RULES.md) explains how the lists and the corpus match.
- [docs/COMMANDS.md](docs/COMMANDS.md), [docs/GROUP_SETTINGS.md](docs/GROUP_SETTINGS.md),
  [docs/CONFIGURATION.md](docs/CONFIGURATION.md), [docs/AI.md](docs/AI.md),
  [docs/CORPUS_FORMAT.md](docs/CORPUS_FORMAT.md), [docs/VERSIONS.md](docs/VERSIONS.md),
  [docs/RUNNING.md](docs/RUNNING.md) and [docs/RELEASING.md](docs/RELEASING.md) cover commands,
  settings, the AI tier, dataset import, running and releases.

## Contributing

Most of the bot can be developed and tested without a WhatsApp account, using the fake client.
See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE). whatsmeow is MPL-2.0.

# Versions and upgrading

How releases are numbered, which image tag to run, and how to upgrade safely.
The full policy is in [DESIGN.md §23](DESIGN.md#23-releases-and-versioning).

## Version numbers

Releases use [SemVer](https://semver.org) tags `vX.Y.Z`. The project stays on **0.x** until the
*operator surface* is frozen: environment variables and config keys, chat commands, the image
contract (entrypoint, ports, volumes, user) and the database upgrade path.

While on 0.x:
- a **patch** release (`0.1.N`) is **fixes only and always safe** to apply;
- a **minor** release (`0.Y.0`) **may break things**. Its release notes start with
  "⚠ BREAKING CHANGES" and the upgrade steps.

Pre-releases look like `v0.1.0-rc.1`. Tags are permanent: a bad release is fixed with a new version,
never by moving a tag.

There's **no long-term-support line**. A WhatsApp protocol change forces everyone onto a new
whatsmeow anyway, so an "old but supported" version would simply stop connecting. Keep your
instance updated.

## Image tags

Images are multi-arch (`linux/amd64`, `linux/arm64`) at `ghcr.io/rayanalyasi/antispam-bot`.

| Tag | Meaning |
|---|---|
| `0.1.3` (`X.Y.Z`) | one exact release; never changes |
| `0.1` (`X.Y`) | the newest **stable** patch of that minor: **the stable channel** |
| `latest` | the newest stable release of any minor; **don't pin it** |
| `0.1.0-rc.1` | a pre-release; it never moves `X.Y` or `latest` |
| `edge`, `sha-<7>` | built from every push to `staging`, for testing only |

There's no bare `0` tag, because 0.x minors may break.

**Pin `:0.1`** to get safe patch releases automatically, or **pin an exact version** and upgrade by
hand. Never run `latest`: it jumps to a new minor, which may break your setup.

## Example `docker-compose.yml`

The repository's `docker-compose.yml` is the full deployment file. What matters for upgrades is
that it pins both images: the bot on the `0.1` stable channel, and Postgres on a fixed major
version. A minimal version looks like this:

```yaml
services:
  bot:
    image: ghcr.io/rayanalyasi/antispam-bot:0.1
    restart: unless-stopped
    env_file: .env
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:18
    restart: unless-stopped
    environment:
      POSTGRES_USER: antispam
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: antispam
    volumes:
      - pgdata:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U antispam -d antispam"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
```

In `.env`, set `POSTGRES_PASSWORD` and point the bot at it with
`DATABASE_URL=postgres://antispam:<password>@postgres:5432/antispam?sslmode=disable`, plus the other
variables the bot needs (`OPERATOR_PHONES`, …). Postgres is never exposed outside the compose network.

Upgrading Postgres to a new **major** version needs a dump and restore, so change `postgres:18` only
on purpose, never with a moving tag.

## Start new groups in shadow mode

v0.1 ships **before** the project's own accuracy gates (the recall report on a spam hold-out set
and a week of measured shadow-mode precision, [DESIGN.md §20](DESIGN.md#20-phased-roadmap)).
So for every new group:

1. Turn on shadow mode in the group with `.mode shadow`. The bot records what it *would* have
   deleted or banned, and acts on nothing.
2. Review what it would have done for a few days, and overturn anything wrong.
3. Switch to `.mode enforce` once you're happy with it.

## Upgrading

Database migrations run automatically on start and are forward-only. You can skip versions within
a major (for example 0.1.2 → 0.3.0 directly), because migrations run in order. A binary refuses to
start on a database from a newer version, so a mistaken downgrade fails safely instead of corrupting
data.

Before a **minor** upgrade, read the release notes' "⚠ BREAKING CHANGES" section and follow its
upgrade steps. Then, for any upgrade:

1. Back up the database:
   ```sh
   docker compose exec postgres pg_dump -U antispam antispam > antispam-$(date +%F).sql
   ```
2. Pull the new image:
   ```sh
   docker compose pull
   ```
3. Restart:
   ```sh
   docker compose up -d
   ```

A short restart is fine: messages that arrive while the bot is down are delivered when it
reconnects.

Patch releases never contain a migration that blocks going back to an earlier patch of the same
minor, so rolling back from 0.1.4 to 0.1.3 keeps working. Rolling back across a minor means
restoring the backup.

## Verifying a release

Every release image and the archives' `checksums.txt` are signed with
[cosign](https://docs.sigstore.dev) (keyless) by the release workflow, and both carry GitHub
build-provenance attestations. SBOMs are attached to the release (archives) and to the images.

Check an image's signature:

```sh
cosign verify ghcr.io/rayanalyasi/antispam-bot:0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/RayanAlyasi/AntiSpam-Bot/\.github/workflows/release\.yml@refs/heads/(main|release/.+)$'
```

Check the archives (download `checksums.txt` and `checksums.txt.sigstore.json` from the release):

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/RayanAlyasi/AntiSpam-Bot/\.github/workflows/release\.yml@refs/heads/(main|release/.+)$'
sha256sum --ignore-missing -c checksums.txt
```

Check the build provenance:

```sh
gh attestation verify oci://ghcr.io/rayanalyasi/antispam-bot:0.1.0 --repo RayanAlyasi/AntiSpam-Bot
gh attestation verify antispam-bot_0.1.0_linux_amd64.tar.gz --repo RayanAlyasi/AntiSpam-Bot
```

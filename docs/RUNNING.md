# Running the bot

The `antispam` binary runs the whole bot. Configuration is covered in
[CONFIGURATION.md](CONFIGURATION.md).

## Start-up

1. It loads and validates the configuration, and stops on any mistake.
2. It connects to `DATABASE_URL`. Every session runs in UTC.
3. It checks the schema version. If the database was migrated by a **newer** release, the bot
   refuses to start and says which migration the database is at and which release it needs.
   Migrations are forward-only, so a downgrade means restoring a backup.
4. It applies any pending migrations, embedded in the binary. Two processes starting at once take
   turns through a Postgres advisory lock. The schema has `app` (application data and River's job
   tables) and `whatsmeow` (the WhatsApp session store).
5. It starts its roles.

## Roles

| Role | What it does |
|---|---|
| `connector` | Owns the WhatsApp connection for each bot number. (Not implemented yet: it connects nothing.) |
| `workers` | Runs background and periodic jobs on River, including a heartbeat job every minute. |
| `api` | Serves HTTP on `HTTP_ADDR`: for now `/metrics`, `/healthz` and `/readyz`. |

All three run in one process by default. Use `-roles` to run a subset, for example
`antispam -roles workers,api`. If any role fails, the others are stopped and the process exits
with status 1.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-roles` | `connector,workers,api` | Comma-separated roles to run. |
| `-shutdown-timeout` | `8s` | How long the roles get to stop after `SIGTERM` or `SIGINT`. It stays under Docker's default 10 s stop grace period. |
| `-check-config` | off | Validate the configuration, print `config loaded` and exit. It doesn't connect to the database. |

Exit status: `0` after a clean shutdown (or a valid `-check-config`), `1` on a configuration,
database or role error, or if shutdown took longer than `-shutdown-timeout`, and `2` on a bad
command line.

## Health and metrics

| Endpoint | Meaning |
|---|---|
| `/healthz` | `200` while the process is serving. Use this for Coolify and Docker health checks. |
| `/readyz` | `200` when the database answers and at least one bot number is connected, otherwise `503`. The JSON body says which check failed: `{"status":"unavailable","database":"ok","bots_connected":0}`. |
| `/metrics` | Prometheus metrics. |

`/readyz` stays `503` until the connector is implemented, because no bot connects yet.

## Logs

Logs are JSON lines on stderr. Each line from a component carries a `module` field, and
`LOG_LEVEL` can set a level per module. Phone numbers are masked wherever they're logged, for
example `+966 5•• ••• •12`.

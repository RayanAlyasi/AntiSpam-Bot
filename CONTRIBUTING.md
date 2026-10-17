# Contributing

Thanks for helping. This guide covers the development setup, the rules the code follows, and
how changes get merged. By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## You don't need a WhatsApp account

Everything the bot does to WhatsApp goes through one narrow interface, `wa.WAClient`
(`internal/wa`). Tests use the recording fake in `internal/wa/fake`, which serves the groups
and users a test sets up, records every call (sends, deletes, removals) and can inject errors
such as rate limits or a temporary ban. So you can build and test detection, scoring, routing,
the action queue, reviews and commands without a phone.

**Never pair, connect or send from a real WhatsApp number while developing,** and never point
tests at one. A mistake can get that number banned. Only the maintainers' staging checklist
(DESIGN.md §18) uses a dedicated test number.

## Setup

- Go (the version in `go.mod`), Docker (for the Postgres integration tests) and
  [golangci-lint](https://golangci-lint.run).
- `sqlc` and the other code generators are pinned in `tools/go.mod` and run through
  `go tool`, so you don't install them separately.

```bash
make setup-hooks   # once per clone: routes git hooks through .githooks
make check-fast    # build, vet, lint, architecture rules and race tests
make check         # everything, including the Postgres integration tests (needs Docker)
make gen           # regenerate sqlc code after changing a query or migration
```

Integration tests use the `integration` build tag and start a throwaway Postgres with
testcontainers. `docker-compose.test.yml` gives you one to poke at by hand.

## How the code is organised

- `cmd/antispam`: the binary. It runs every role (connector, workers, api) by default.
- `internal/wa`: the `WAClient` interface. `internal/wa/adapter` is the only package that
  imports whatsmeow, and `internal/wa/fake` is the test double.
- `internal/envelope`, `internal/normalize`, `internal/detect`, `internal/scoring`,
  `internal/policy`: the pipeline from a raw message to a decision. These domain packages don't
  import pgx or River; storage and queues sit behind interfaces.
- `internal/actions`: the paced action queue, the only code that performs WhatsApp writes.
- `internal/store`: Postgres access (pgx + sqlc). Migrations live in `migrations/`.

`docs/DESIGN.md` is the specification. If you change behaviour, configuration, commands or
deployment, update the user-facing docs (README, `docs/`) in the same pull request.

## Code rules

- Take a `context.Context` first on every path that does I/O. Wrap errors with `%w`.
- Log with zerolog only, and mask phone numbers in logs.
- Domain code gets time from an injected clock, never `time.Now()`, so windows and pacing can
  be tested with `testing/synctest`.
- Tests: table tests for most things, property tests (`pgregory.net/rapid`) for the decision
  invariants in DESIGN.md §18, and fuzz tests for parsers and normalizers. Failure messages say
  what was expected and why it matters.
- **No real message data in the repo.** Fixtures are synthetic, or redacted with consent.
  When you report a new spam pattern, attach redacted examples only.

## Branches, commits and pull requests

- Branch from `staging` as `feat/<short-name>` and open the pull request into `staging`.
  `main` only receives release pull requests from `staging`. A maintainer merges every PR.
- Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org):
  `<type>: <description>`, where type is `feat`, `fix`, `perf`, `refactor`, `docs`, `test`,
  `build`, `ci` or `chore`. The description is lowercase, imperative and at most 72
  characters. Mark a breaking change with `!` and explain the upgrade in a `BREAKING CHANGE:`
  footer. The changelog and version numbers are generated from these.
- Feature PRs are squash-merged, so the PR title becomes the commit. Fill in the PR template.
- CI must be green: build, lint, architecture rules, unit, property, integration and replay
  tests.

## Security

Please don't open public issues for vulnerabilities. Use GitHub's private vulnerability
reporting on this repository instead.

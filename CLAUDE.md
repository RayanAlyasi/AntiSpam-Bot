# AntiSpam Bot

A self-hosted, open-source WhatsApp anti-spam bot in Go, built on whatsmeow, PostgreSQL and River.
The spec is `docs/DESIGN.md`. Read the sections a task points to; don't load the whole file.
How this repo is built by agents: `docs/AGENTIC_ENVIRONMENT.md`.

The old Node/Baileys code (`index.js`, `src/`, `node_modules/`) is legacy and is being replaced.
Don't extend it. Never read `auth_info_baileys/` or `whatsapp_logs.db`: they hold a real
WhatsApp session and personal messages.

## Commands

- `make check-fast`: build, vet, lint (new issues), architecture, race tests, acceptance, tamper audit. Terse output; full logs in `.agent/logs/`.
- `make check`: the full gate, including integration tests (needs Docker).
- `make gen`: sqlc code generation. `make check-gen` fails if generated code is stale.
- `go doc <pkg>[.<Symbol>]`: the source of truth for library APIs (whatsmeow changes often). See `/whatsmeow-api`.

## How work happens

One feature per session, through `/next-feature`. State lives on disk, not in memory:
`harness/features.json` (the plan), `harness/progress/<ID>.md` (one log per feature),
`harness/dead-ends/` (approaches that failed; read before retrying something), `.agent/plan.md`
(current checklist). Logs are one file per feature, so parallel sessions never edit the same file.

Done means: `make check` is green, the judge agent returned PASS (its stamp is what allows the
commit), and `harness/features.json` has `passes: true` with evidence.

## Boundaries (enforced by hooks; these explain why)

- `acceptance/` is hidden from the builder. The tests there were written from the contract
  without seeing the implementation, so they can't be tailored to it. Run them with
  `make check-fast` and read the results.
- Committed tests, `testdata/fixtures/`, `harness/evals/`, `.claude/`, `.githooks/`, the Makefile
  and the lint and architecture configs can't be edited by agents. If a test or contract looks
  wrong, use `/test-change-request` and stop. A weakened test hides the bug it was written to catch.
- Never pair, connect or send from a real WhatsApp account. Everything goes through the `WAClient`
  interface with the fake client. A real-number mistake can get the number banned.
- Branches: `feat/*` → PR into `staging` → release PR into `main`. Agents push only `feat/*`
  branches and open PRs with `--base staging`. GitHub rejects direct pushes to `main` and
  `staging`, and a human merges every PR.

## Commits and pull requests

Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org):
`<type>: <description>`, where type is `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`,
`ci` or `chore`. The description is lowercase, imperative and 72 characters or fewer, e.g.
`feat: add recursive unwrapper for FutureProof wrappers`. Mark a breaking change with `!`
(`feat!: rename review.timeout to review.wait`) and explain the upgrade in a `BREAKING CHANGE:` footer.
release-please reads these to write the changelog and pick the next version, so the type matters.

- **Body:** after a blank line, what changed and why, wrapped at 72 characters. Put the feature ID in a
  footer: `Feature: F-012`.
- **PRs:** the title uses the same format (feature PRs are squash-merged, so the title becomes the
  commit). The body follows `.github/pull_request_template.md`.
- **Docs:** if a change alters behaviour, configuration, commands or deployment, update the
  user-facing docs (README, `docs/`) in the same PR.
- No `Co-Authored-By` trailers, no "Generated with" lines, and no mention of AI or agents.

## Working style

- Make surgical changes that serve the current feature. If you notice a pre-existing bug, add it
  to the "Follow-ups" section of `harness/progress/<ID>.md` instead of fixing it now.
- Write general solutions. Never special-case code to make a particular test pass.
- Size new tests like the neighbouring tests, and prefer table tests.
- Match the surrounding code's naming, error handling and comment density.
- Don't delegate work you can finish in a few tool calls, and don't use subagents to verify your
  own work (the judge does that). For a single lookup, use grep or the Go language server directly.
- When something is ambiguous in `docs/DESIGN.md`, pick the more conservative option, note it in
  `harness/deviations/<ID>.md`, and keep going.

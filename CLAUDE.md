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
`harness/features.json` (the plan), `harness/progress.md` (log), `harness/dead-ends.md`
(approaches that failed; read before retrying something), `.agent/plan.md` (current checklist).

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
- Pushes go to `feat/*` branches only. Merging is done by a human.

## Working style

- Make surgical changes that serve the current feature. If you notice a pre-existing bug, add it
  to `harness/progress.md` as a follow-up instead of fixing it now.
- Write general solutions. Never special-case code to make a particular test pass.
- Size new tests like the neighbouring tests, and prefer table tests.
- Match the surrounding code's naming, error handling and comment density.
- Don't delegate work you can finish in a few tool calls, and don't use subagents to verify your
  own work (the judge does that). For a single lookup, use grep or the Go language server directly.
- When something is ambiguous in `docs/DESIGN.md`, pick the more conservative option, note it in
  `harness/deviations.md`, and keep going.

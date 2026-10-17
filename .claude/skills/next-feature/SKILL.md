---
name: next-feature
description: Builds exactly one feature from harness/features.json end to end (contract and hidden tests, implementation, gate, judge, commit, PR, scorecard). Use when the user says /next-feature, optionally with a feature ID.
argument-hint: "[F-xxx]"
disable-model-invocation: true
---
Build one feature, then stop. Work through the checklist in order. Copy it into `.agent/plan.md` first, and tick items as you finish them. If you stop early, write `BLOCKED: <reason>` at the top of that file. The Stop hook reads it, and continues the session when items are open and no blocker is written.

## Checklist

- [ ] **1. Orient.** Read `git log --oneline -15`, the last 30 lines of `harness/progress.md`, all of `harness/dead-ends.md`, and any open `harness/test-change-requests/`. Run `make check-fast`. If it's red before you've changed anything, fix that first, or write BLOCKED.
- [ ] **2. Pick.** Requested feature: $ARGUMENTS. If none was given, use the first feature in `harness/features.json` with `passes: false` whose `depends_on` features all pass. Create the branch from an up-to-date `staging`: `git switch staging && git pull --ff-only && git switch -c feat/<ID>-<short-slug>`.
- [ ] **3. Contract.** Delegate to the **architect** agent with the feature ID. It writes `.agent/contracts/<ID>.md` and the acceptance tests, and commits them.
  - If the feature is `core: true`, or fewer than 10 features have `passes: true`, show the user the contract and wait for approval before building. During unattended runs (no user present) it proceeds without approval.
  - If the whole change fits in one sentence, the architect may write only the contract with no acceptance tests. Note that in the contract.
- [ ] **4. Build.** Read the contract (not the tests; they're hidden from you) and the DESIGN.md sections it cites. Implement with your own unit, table, property and fuzz tests, as `.claude/rules/tests.md` describes. Use `/whatsmeow-api` for library questions. Iterate until `make check-fast` is green. If the feature changes behaviour, configuration, commands or deployment, update the user-facing docs (README, `docs/`) in the same change.
  - **Effort:** core packages (queue, identity, scoring, bans) deserve careful reasoning.
  - **Caps:** if the same gate failure happens twice in a row, step back and reconsider the approach. On a third identical failure, write BLOCKED. Don't thrash.
  - **If an acceptance test seems wrong**, use `/test-change-request` and stop. Never work around a test.
- [ ] **5. Record.** In `harness/features.json`, set this feature's `passes: true`, and set `evidence` to the test names that prove it. Append to `harness/progress.md`: date, ID, what was built, anything deferred. Then stage everything: `git add -A`.
- [ ] **6. Judge.** Delegate to the **judge** agent with the feature ID, the contract path, and "first review" or "re-review".
  - **PASS:** a hook writes the stamp; continue.
  - **FAIL:** fix only the blocker and major findings, run `make check-fast`, `git add -A`, then ask for a re-review. At most 3 judge rounds; after that, write BLOCKED with the open findings.
  - **UNKNOWN:** write BLOCKED with the judge's reason.
- [ ] **7. Commit and PR.** Commit exactly what's staged. The stamp covers that diff, so any further change needs a re-review. The message follows CLAUDE.md's "Commits and pull requests": a Conventional Commit subject (`feat: …` for new behaviour, `fix: …` for a bug), a why-focused body and a `Feature: <ID>` footer. Push with `git push -u origin HEAD`, then `gh pr create --base staging`. Use the commit subject as the PR title, and a body following `.github/pull_request_template.md`, mentioning the contract, the acceptance tests and the judge's minor/nit findings. If `gh` or the remote isn't available, say so and continue.
- [ ] **8. Score.** Run `harness/scripts/scorecard.py <ID>`.
- [ ] **9. Retro.** Delegate to the **retro** agent with the feature ID. If it wrote files, commit them as `chore: record retro notes for <ID>` (bookkeeping commits don't need a stamp) and push.
- [ ] **10. Finish.** Delete `.agent/plan.md`. Report in under 10 lines: the feature, the PR link, the judge rounds, anything deferred. Merging is the user's job.

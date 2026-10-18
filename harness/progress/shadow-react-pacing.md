# Shadow-react pacing

Branch `feat/shadow-react-pacing`, PR into `staging`. Fast path (no `/next-feature`, no contract),
asked for by the maintainer, who is live-testing PR #66 and found the reactions far slower than
real deletes would be.

## Done

- A shadow reaction stands in for the action it reports, so it gets that action's timing:
  - `Posting.rule` (`internal/actions/deps.go`): an allowed shadow react is not paced by the
    per-group post cap and doesn't count against it. The per-chat window (10/min), the global
    ceiling and spacing still apply.
  - `shadowPriority` (`internal/pipeline/pipeline.go`): queued at the priority of the most
    urgent would-be action (P1 when a delete would run, P3 when only a review would, P4 if none).
  - `within` (`internal/actions/executor.go`): a shadow react is never "urgent" despite its
    10-minute deadline, so it takes its round-robin turn and can't hold back real deletes or
    starve OTPs and reviews (the judge's first-pass blocker).
- Tests: `internal/actions/shadowpace_test.go`, `internal/pipeline/shadowprio_test.go`. Five
  existing tests pinned 1/min and P4; the maintainer applied the replacement with approval record
  `harness/test-approvals/2026-10-18-shadow-react-pacing.md` (commit `test: expect delete pacing
  for shadow reactions`).
- Docs: DESIGN §8.4, §10.3; docs/GROUP_SETTINGS.md; docs/action-queue.md.

## Follow-ups

- `within` treats every action with a deadline under `UrgentWithin` (1 h) as urgent, including
  optional warning sends (10 min, P3) and edits. With warnings on, they could jump ahead of
  reviews and OTPs within P3. Pre-existing; not fixed here.
- The architect agent may not write `harness/proposed/`, so a test-change patch it drafts has to
  be saved by another agent.

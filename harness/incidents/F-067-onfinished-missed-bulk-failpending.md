# F-067 follow-up: OnFinished hook skipped actions failed in bulk

Symptom: judge round 1 FAIL (major). After an ErrNotAdmin, the executor's `react(classNotAdmin)` called `Store.FailPending`, which moved every pending admin-kind action in the chat to `failed`. None reached `Deps.OnFinished`, though the doc said the hook hears about every finished action. A consumer waiting on a kick or delete outcome would wait forever.

Root cause: the hook was wired into `finish` (the single-action path) only. The store had a second path that finalises many rows in one statement and returns nothing.

Evidence: .agent/logs/judge-F-064.md section "F-067 follow-up", round 1; scratch test onfinished_once_scratch_test.go (second delete failed, reported 0 times). Fixed by FailChat / `FailPendingActionsReturning` (round 2 PASS). Other round-1 minors: hook ran under callMu at the mid-batch expiry, ExpireDue reports used the cancellable run ctx, no deadline on the hook, test keyed a map by ID so a double report could not fail it.

Prevention: when adding a "told about every X" hook, list every code path that sets the final status (grep the store for status writes, including bulk UPDATEs) before writing the doc line, and make the test count reports per ID and compare with the store.

Follow-up (round 2 minor): after a fenced Finish rejection the old executor may read the new owner's final row and report it too, so delivery is at-least-once. The hook must be idempotent; say so in the Deps doc.

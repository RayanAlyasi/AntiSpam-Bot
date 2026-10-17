# F-064 bundle: three judge rounds, majors in executor concurrency and fairness

Symptom: the judge needed 3 rounds on the WP-H action queue (PR #22).
- Round 1 majors: temp-ban retry loop; per-priority candidate window starving other groups; greedy batch packing; kill switch not re-read inside batches; 429/ban races between reads and writes.
- Round 2 majors: a write's 429 applied after releasing the call lock (a following Read could slip through); kill switch stale behind a slow Read; one huge kick (uncapped) starving other groups.
- Round 3 (PASS) minors: a yield re-Claims and burns an attempt each turn, so a multi-turn kick exhausts its retry budget with no error; the deadline is not re-checked after lockCall waits behind a slow Read; a yield checks the group window but not the bot-wide write bucket (P2 batch delays a P0 lockdown ~2 min while slowed); stale deviation text; alert sent while callMu is held.

Root cause: the executor shares one call lock, a pause/breaker state and a control state across reads, writes, batches and yields. Each fix (apply 429 under the lock, re-read control under the lock, cap calls per run) opened the next neighbouring gap, because the invariants were never written down as one list: (1) state changes from a response are visible before the lock is released, (2) every check (deadline, kill switch, breaker, pause) is repeated after every wait, (3) any long-running unit yields on every shared limiter, not one, and (4) a yield keeps progress without charging a retry attempt. Round 3 findings are violations of 2, 3 and 4 that rounds 1 and 2 only fixed in the specific spot probed.

Evidence: .agent/logs/judge-F-064.md (round 3 report; probes in .agent/scratch/F-064/judge*_test.go), .agent/logs/judge.jsonl; commit ab3f3c8 and the merge a4ea2e6 on feat/wp-h-action-queue.

Prevention: before the first judge run, write the executor's invariants as a checklist (state set before unlock, re-check after every wait, yield on every limiter, yield does not cost an attempt) and add a test per invariant for each call path (single, batch, participants, Read). The contract said "past-deadline actions are never executed" and "timeouts retry"; both were violated through paths the tests did not cover.

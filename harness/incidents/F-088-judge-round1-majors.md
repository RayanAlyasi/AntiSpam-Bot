# F-088 bundle: judge round 1 FAIL with several majors

- Symptom: judge FAIL on the first round. Majors: (1) settings went stale across processes because nothing listened for NOTIFY; (2) double count on the rails (NoteBans); (3) a ban recorded with no person ID; (4) redelivery was checked after evaluation, so a redelivered message was evaluated twice; plus other majors.
- Root cause: the end-to-end wiring was written feature by feature without a pass over cross-process and at-least-once behaviour. Dedup/idempotency was placed after the costly or side-effecting step, and counters were incremented at both caller and callee.
- Evidence: `.agent/logs/judge.jsonl`, `.agent/logs/judge-F-088.md` (later rounds list status), commit 782afb6.
- Would have prevented: a pre-judge checklist for wiring features: dedup first, one owner per counter, every persisted ban/review row carries the person ID, and any cached setting has an invalidation path (NOTIFY) tested with two processes. Related: WP-F-pre-judge-reliability-checklist.md, WP-K-entry-point-invariants.md.

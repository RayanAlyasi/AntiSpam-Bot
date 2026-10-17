# WP-G: tamper audit diffed against a stale local staging ref

- Symptom: the tamper audit flagged human-approved test fixes from #21 as tampering. It stayed red until a human fast-forwarded local `staging`.
- Root cause: the local `staging` ref is shared by all worktrees and checked out in the main checkout, so the builder cannot update it, and the audit diffs against it instead of `origin/staging`.
- Evidence: .agent/logs/check-tamper.log; .agent/logs/judge-WP-G-merge.md (#21 not yet in the merge).
- Would have prevented: the audit using a freshly fetched origin/staging (already proposed: harness/proposals/WP-I-tamper-base-origin.md). Repeat of the WP-I incident.

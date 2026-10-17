# WP-F: tamper audit diffed against a stale local staging ref

- Symptom: after merging origin/staging, the audit flagged human-approved upstream test edits as tampering. With a merge in progress it diffed against the old merge base, so the stamp could not be written.
- Root cause: the audit uses the local `staging` ref, which was stale and checked out in the main checkout. Repeat of F-106-tamper-flags-staging-merge and WP-I-harness-friction-merge-tamper-guard; proposal WP-I-tamper-base-origin already exists.
- Evidence: .agent/logs/check-tamper.log; .agent/logs/judge.jsonl; commit 6859cef.
- Workaround: restacked as one commit on staging (git apply --3way of the staged diff) and asked the parent to fast-forward staging.
- Prevention: audit against fetched origin/staging, or fast-forward local staging before starting.

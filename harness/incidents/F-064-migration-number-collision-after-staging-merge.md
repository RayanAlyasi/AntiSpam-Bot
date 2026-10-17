# F-064/F-065 (WP-H Postgres): migration number collided with another package's after a staging merge

Symptom: the actions migration was numbered 00004, but staging gained another package's 00004 (group settings, #27) while the branch was open. A first fix used 00064 (feature ID), which broke a committed test that requires contiguous numbering. Final number 00007 (commit 616c517; judge round 6 PASS on the merge resolution). Side effect: a local dev DB that applied the old branch-only 00004_actions would be skipped by goose and fail on 00007 (reset local DBs).

Root cause: migration numbers are one global sequence but are picked on the branch at start time; parallel feature branches pick the same next number. Nothing told the builder that numbers must be contiguous; the test said so only after failing.

Evidence: commits f9c5b40 (merge) and 616c517 (renumber) on feat/wp-h-postgres-wiring; .agent/logs/judge-F-064.md round 6 nit.

Prevention: state the rule in the migrations rules (next number = highest on origin/staging + 1, contiguous, re-check after every staging merge).

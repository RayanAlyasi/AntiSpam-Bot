# Proposal: wiring features run an idempotency checklist before the judge

Rule: before the judge, check each end-to-end path for: redelivery/dedup before evaluation, one owner per counter, a person ID on every persisted ban or review row, and an invalidation path (for example NOTIFY) for any cached setting read by another process.

Reason: judge round 1 on the fast lane failed on exactly these four, each a cheap check.

File: `.claude/rules` (reliability rules); extend WP-F-pre-judge-reliability-checklist.md.

Incident: harness/incidents/F-088-judge-round1-majors.md

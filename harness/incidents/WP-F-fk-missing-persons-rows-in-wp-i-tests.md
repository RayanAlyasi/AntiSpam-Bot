# WP-F: WP-I committed tests insert person IDs without persons rows

- Symptom: the FKs that WP-I's migrations promised could not be added; the merge uses a column-name rule instead.
- Root cause: committed tests are locked and seed person IDs with no persons rows.
- Evidence: WP-I tests on staging; commit 6859cef.
- Prevention: check at contract review that fixtures satisfy promised FKs; otherwise use /test-change-request early.

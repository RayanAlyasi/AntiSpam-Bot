# F-117 (WP-L): judge round 2 FAIL, double confirm then overturn left an entry known

- Symptom: after confirm, confirm, overturn, the corpus still treated the message as known spam.
- Root cause: `EntryFor` picked an entry without choosing the newest non-retracted one; the overturn retracted only one of two live rows. State transitions were tested one at a time, not as sequences.
- Evidence: `.agent/logs/judge-F-088.md` round 2 (superseded by later rounds), `overturn2_integration_test.go` added in response, commit 782afb6.
- Would have prevented: a sequence/property test over confirm, overturn, re-confirm orderings for any "latest state wins" store, written with the first implementation. See F-064/WP-I-dedupe-live-rows-only.md for the related live-rows rule.

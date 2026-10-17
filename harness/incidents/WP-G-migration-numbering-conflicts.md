# WP-G: migration numbering conflicts between shared notes, wp-c's test and staging

- Symptom: WP-G was given 00030-00049 in the shared notes, but wp-c's `TestMigrateFreshDatabase` required gap-free versions, so WP-G used 00004-00006 for a while. Staging itself briefly had two migrations with version 8 (#31 and #32), fixed by #34. WP-G was moved back to 00030-00032 after #21 changed the test to count migrations. goose has no out-of-order, so dev databases that ran 00030 before staging's 00011/00013 must be recreated.
- Root cause: ranges were reserved in prose while a committed test enforced contiguity; parallel packages took the same number because nobody owned allocation.
- Evidence: harness/deviations/F-041.md (Migrations bullet); harness/progress/F-041.md; commits 0ca86ae (#34), 32d206b (#21).
- Would have prevented: one enforced rule for assigning numbers (next free number from fresh origin/staging at merge time). See proposals F-064-migration-numbers-from-staging.md and F-106-migration-number-allocation.md. Third occurrence (F-064, F-106, WP-G).

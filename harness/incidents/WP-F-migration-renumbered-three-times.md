# WP-F: migrations renumbered three times; staging got two version-8 migrations

- Symptom: a per-package range plan (00010-00029 etc.) clashed with TestMigrateFreshDatabase, which needs contiguous versions. WP-F renumbered (00004, then 00007, then 00009) as other packages landed first. Staging ended with two version-8 migrations (#31 and #32).
- Root cause: numbers were allocated at plan time, not at merge time. Repeat of WP-I-migration-ranges-vs-contiguous-test, F-106-migration-numbering-collision and F-064-migration-number-collision-after-staging-merge. Proposals F-106-migration-number-allocation and F-064-migration-numbers-from-staging already cover the rule, so none is added here.
- Evidence: .agent/logs/check-test.log; PRs #28, #31, #32; commit 6859cef.
- Prevention: take max(version on fetched origin/staging)+1 right before the final commit and re-run the migration test after each staging merge. The duplicate version-8 on staging suggests a duplicate-version check before merge, which would be a harness change.

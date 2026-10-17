# WP-I: migration number range scheme conflicts with TestMigrateFreshDatabase

Symptom: the shared-notes scheme (reserve migration ranges per work package) conflicts with wp-c's committed TestMigrateFreshDatabase, which requires contiguous versions. F-054 migrations ended up as 00005/00006 and the judge had to check the progress logs for the right numbers.
Root cause: a coordination scheme was proposed without reading the constraint in the committed test.
Evidence: .agent/logs/judge-F-054.md; migrations 00005 and 00006.
Prevention: parallel work packages take the next contiguous number at merge time. Check committed tests before proposing cross-session conventions.

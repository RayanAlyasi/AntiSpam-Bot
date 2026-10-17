# Proposal: allocate migration numbers from staging and open PRs

- Rule: "Choose the next migration version as max(staging plus open PRs)+1 with no gaps and re-check before commit, because TestMigrateFreshDatabase requires gap-free versions and parallel packages collide."
- File: the database/migrations rules file.
- Incident: harness/incidents/F-106-migration-numbering-collision.md

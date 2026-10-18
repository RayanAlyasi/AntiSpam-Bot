# Proposal: regenerate sqlc output after a merge, never hand-resolve

Rule: on conflicts in generated files (models.go, sqlc code), take staging's version, renumber your migration, then run `make gen` and `make check-gen`.

Reason: hand-resolved generated code drifted from migrations and repeated conflicts consumed many gate runs.

File: migrations/workflow rules under `.claude/rules`.

Incident: harness/incidents/F-088-staging-merge-churn.md

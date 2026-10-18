# F-088 bundle: dependency waits and merge churn with staging

- Symptom: repeated merges of origin/staging; conflicts in go.mod and models.go (sqlc output); a duplicate migration number on staging (fixed upstream by #34); wall-clock tests flaking under load (fixed upstream by #25). Some work waited on dependency PRs.
- Root cause: parallel bundles share generated files and the migration sequence; the bundle started before its dependencies merged. Recurrence of F-064 migration collisions.
- Evidence: `git log origin/staging..HEAD` (merge commits 42e3326, 7a76374), `.agent/logs/gates.jsonl` (74 runs).
- Would have prevented: starting after dependency PRs merge, regenerating models.go with `make gen` rather than hand-resolving, and renumbering migrations from staging after every merge.

# F-002: `make fmt` rewrote a hidden test the builder cannot restore

- Symptom: `golangci-lint fmt ./...` (same as `make fmt`) modified `acceptance/config/config_test.go`. The guard blocked restoring it. It shows as ` M` in git status and stayed out of the commit.
- Root cause: the formatter runs over the whole tree, including the protected hidden tests. The guard is right to block edits, but nothing stops the formatter from making the edit first, so the builder ends with a dirty protected file it cannot clean.
- Evidence: `.agent/logs/guard.jsonl`; git status at session start (`M acceptance/config/config_test.go`); branch feat/F-002-config-yaml.
- Would have prevented: formatter config or `make fmt` that excludes the hidden-test directory, or formatting only the paths the builder changed (`golangci-lint fmt <paths>`).

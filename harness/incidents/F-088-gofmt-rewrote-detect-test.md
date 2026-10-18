# F-088 bundle: `gofmt -w internal` rewrote another package's committed test (tamper hit)

- Symptom: first gate pass failed tamper (and test). `internal/detect/detect_test.go`, a committed test outside this bundle, showed as modified.
- Root cause: `gofmt -w internal` formatted the whole subtree, including tests owned by other features. Same mechanism as F-002-fmt-rewrote-hidden-test.md (recurrence).
- Evidence: `.agent/logs/check-tamper.log`, `.agent/logs/gates.jsonl` (first run), branch feat/F-088-fast-lane.
- Would have prevented: formatting only the paths this feature changed (`gofmt -l <files>` / `golangci-lint fmt <paths>`), or restoring with `git checkout -- <file>` before the gate.

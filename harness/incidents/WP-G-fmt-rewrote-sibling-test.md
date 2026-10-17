# WP-G: golangci-lint fmt reformatted a sibling's committed test, guard refused the restore

- Symptom: `golangci-lint fmt` over the builder's packages also reformatted internal/detect/detect_test.go (committed by another package). The guard then refused `git checkout` of that file.
- Root cause: the formatter touches every file in a package, including protected committed tests; the guard blocks restores as well as edits, so the builder cannot clean up. Same as F-002-fmt-rewrote-hidden-test.
- Evidence: .agent/logs/guard.jsonl; harness/incidents/F-002-fmt-rewrote-hidden-test.md.
- Would have prevented: formatting only changed non-test paths (proposal F-002-format-only-changed-paths.md), or letting the guard allow restoring a protected file to HEAD.

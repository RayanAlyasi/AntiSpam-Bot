# WP-F: go.mod conflict resolution silently downgraded golang.org/x/time

- Symptom: every staging merge conflicted on go.mod and needed a new judge stamp (merge commits are stamped). One resolution downgraded golang.org/x/time; the judge caught it.
- Root cause: one side of go.mod was taken without comparing versions or running go mod tidy. Related: F-034-merge-staging-before-judge.
- Evidence: .agent/logs/judge-F-020.md; commit 6859cef.
- Prevention: after resolving, run go mod tidy and check `git diff origin/staging -- go.mod` for any version that went down.

# F-106: architect commit blocked by other staged changes

- Symptom: the architect's commit was blocked.
- Root cause: the stamp check covers the whole index, and the shared index held other staged changes.
- Evidence: .agent/logs/guard.jsonl.
- Prevention: check `git diff --cached --stat` is empty of unrelated files before committing. Unstage anything not part of the feature.

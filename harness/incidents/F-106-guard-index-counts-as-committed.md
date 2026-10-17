# F-106: guard blocked editing a staged, never-committed test

- Symptom: guard.py refused to let the builder fix its own wrong assertion; a human had to unstage the file.
- Root cause: `is_tracked` consulted the git index, so a staged-only test counted as a committed (protected) test.
- Evidence: .agent/logs/guard.jsonl. Fixed in #19.
- Prevention: protection should be based on HEAD, not the index (done in #19). Builders should not `git add` tests before the first commit.

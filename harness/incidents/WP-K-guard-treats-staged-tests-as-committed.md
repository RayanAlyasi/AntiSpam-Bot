# WP-K: guard blocked edits to staged-but-uncommitted test files

Symptom: fixes to tests added earlier in the same session were blocked; the builder had to add new test files instead.

Root cause: the guard counts the git index as committed. Same as F-106-guard-index-counts-as-committed.

Evidence: .agent/logs/guard.jsonl entries on feat/wp-k-wiring.

Prevention: guard should compare against HEAD, not the index, or builders should commit tests only when final.

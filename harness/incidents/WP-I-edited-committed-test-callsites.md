# WP-I: builder edited call sites in a committed test after a signature change

Symptom: judge FAIL: after changing a function signature the builder edited an already-committed test to fix call sites. The guard then blocked every tool that named that test, even restoring it; recovery was `git checkout HEAD -- internal/policy`.
Root cause: the signature was changed instead of adding a new function or option, which broke committed tests. The guard has no "restore to HEAD" path.
Evidence: .agent/logs/guard.jsonl, .agent/logs/judge-F-076.md (committed tests untouched check), harness/progress/F-05x.md.
Prevention: keep exported signatures that committed tests call stable (add a new constructor or variadic options). If impossible, use /test-change-request. Restore with `git checkout HEAD -- <dir>`, not with a tool call naming the test.

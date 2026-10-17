# WP-K: judge PASS not stamped because wall-clock tests in another package failed under load

Symptom: judge returned PASS but the stamp hook did not stamp. The hook re-runs make check-fast, and wall-clock bound tests in internal/normalize failed while the machine was loaded. Stamps arrived only after load dropped.

Root cause: timing assertions in an unrelated package sit in the stamp path. Repeat of F-064-loaded-machine-flaky-wallclock-tests. Fixed later by #25.

Evidence: .agent/logs/judge.jsonl (PASS entries without matching stamps), .agent/logs/check-test.log.

Prevention: no wall-clock assertions in tests (harness/proposals/F-106-no-wallclock-assertions.md); builder waits for a quiet machine before asking for the stamp.

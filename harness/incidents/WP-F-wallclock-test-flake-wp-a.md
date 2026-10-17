# WP-F: wall-clock-bound WP-A test flaked under full parallel load

- Symptom: intermittent failure in the full run, passing alone.
- Root cause: asserts on real elapsed time. Repeat of F-064-loaded-machine-flaky-wallclock-tests and F-106-wallclock-tests-flake-under-load.
- Evidence: .agent/logs/check-test.log.
- Prevention: fake clock or testing/synctest; raise via /test-change-request since committed tests are locked.

# holdout-recall: growth-ratio timing tests in normalize, redact and urls flaked under load

Symptom: linear-time growth tests failed intermittently during make check with machine load around 16.

Root cause: wall-clock ratios between input sizes are noisy under load even for linear code. Repeat of WP-K-stamp-blocked-by-load-flaky-tests; the tests are committed, so the builder can't change them.

Evidence: .agent/logs/check-test.log, harness/progress/holdout-recall.md.

Prevention: no wall-clock assertions (harness/proposals/F-106-no-wallclock-assertions.md); rerun on a quiet machine, or file a test change request to count operations instead.

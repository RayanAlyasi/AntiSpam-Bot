# WP-G: wall-clock test bound flaked under heavy machine load

- Symptom: wp-a's `internal/normalize/redact` TestTextBounded (3 s bound, -race) took 3.1-3.4 s while many sessions ran tests; it passes alone.
- Root cause: elapsed-time assertion on a shared, loaded machine. #25 already moved the normalize linear-time test to growth checks, but this one remained.
- Evidence: harness/progress/F-041.md follow-ups; harness/incidents/F-064-loaded-machine-flaky-wallclock-tests.md.
- Would have prevented: proposal F-106-no-wallclock-assertions.md applied to the leftover test (committed tests are human-owned, so it needs /test-change-request).

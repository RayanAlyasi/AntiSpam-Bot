# Approve the linear-time test fixes

**Date:** 2026-10-17 · **Approved by:** maintainer

branch: feat/linear-time-tests

- internal/normalize/redact/redact_test.go
- internal/normalize/text_test.go
- internal/normalize/urls/urls_test.go

**Why:** these tests asserted a 3s wall-clock limit on large hostile inputs, so they failed under -race
whenever the machine was busy (4-5s at load average 15-20 with the parallel build sessions) and blocked
unrelated packages' checks. They now use normtest.Linear, which compares an input with one four times
larger: it still catches quadratic work but doesn't depend on machine load.

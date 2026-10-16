---
paths:
  - "**/*_test.go"
  - "acceptance/**"
---
# Tests

- [T-001] Tests never talk to real WhatsApp. Use the fake `WAClient`.
- [T-002] Use `testing/synctest` for anything time-based (pacing, windows, deadlines). No `time.Sleep` in unit tests.
- [T-003] Integration tests that need Postgres use the `integration` build tag and testcontainers.
- [T-004] Failure messages say what was expected and why it matters: `got X, want Y (ask-admins mode must never auto-ban)`.
- [T-005] Use property tests (`pgregory.net/rapid`) for DESIGN.md §18 invariants, and fuzz tests for parsers and normalizers.

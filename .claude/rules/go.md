---
paths:
  - "**/*.go"
---
# Go

- [GO-001] Pass `context.Context` as the first parameter on every path that does I/O, and respect cancellation.
- [GO-002] Wrap errors with `%w` and context (`fmt.Errorf("revoke %s: %w", id, err)`). Never drop an error from Postgres, River or whatsmeow.
- [GO-003] Log with zerolog only (no `fmt.Print*` or `log.*`). Mask phone numbers in logs; they are personal data.
- [GO-004] Only `internal/wa` may import `go.mau.fi/whatsmeow`. Everything else talks to WhatsApp through the `WAClient` interface, so the fake client can stand in for it in tests.
- [GO-005] Domain packages (detection, scoring, policy) must not import pgx or River. They receive data and return decisions; storage and queues live behind interfaces.
- [GO-006] Time comes from an injected clock, never `time.Now()` directly in domain code, so pacing and windows can be tested with `testing/synctest`.

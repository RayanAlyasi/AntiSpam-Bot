# F-002: River start with a cancelled context hung Stop; synctest hang

- Symptom A: River started with a cancelled context left its notifier retrying in a tight loop, so `Stop` never finished.
- Symptom B: a `testing/synctest` test hung because its channel was created outside the bubble.
- Root cause A: River ties its background goroutines to the context passed to Start. Cancelling it does not stop them cleanly. Root cause B: channels made outside the bubble are not bubble-aware, so the durable block never resolves.
- Evidence: `harness/progress/F-002.md`; `.agent/logs/check-test.log`.
- Fix A: start with `context.WithoutCancel(ctx)` and call Stop explicitly on shutdown. Fix B: create channels inside the `synctest.Test` func.
- Would have prevented: the River doc and a note in the Go rules about both pitfalls.

# F-069/F-074: breaker signal edge cases found by the judge (minors, fixed or accepted)

Symptom: judge round 1 minors on internal/actions/runner.go.
1. Each KeepAliveTimeout and Disconnected counted as a separate warning sign. One outage right after one write (timeouts at +20 s, +50 s, then Disconnected) opened the breaker, which only an operator can close and has no UI yet (probe runner_keepalive_scratch_test.go).
2. `Trip` used an executor left in the map after its Run returned (standby process, or ErrExecutorRunning). `OpenBreaker` then persisted a fresh default rate over the real owner's slowed rate.
3. docs/action-queue.md did not mention stream-replaced, the disconnect rule, or how to close the breaker.

Root cause: signals were mapped one event to one sign without treating an outage as one incident; "an executor exists in the map" was taken to mean "it owns the lock".

Evidence: .agent/logs/judge-F-064.md "WP-H wiring" round 1. Opening the breaker is the conservative side, so these were minor.

Prevention: for event-to-counter mappings, write the outage case (one incident, many events) as a test first; code that writes shared persisted state must check it holds the lock, not only that an object exists.

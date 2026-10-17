# F-064/F-065 (WP-H Postgres): payload scrub reverted because a hidden test reads the finished payload

Symptom: the builder tried to scrub an action's payload when it finishes. A hidden test reads the finished payload, so the scrub failed it and was reverted. Only the NUL-byte strip survived (the accepted fix for the Postgres insert failing on NUL; see judge round 6 notes on TestJudgePGPayloadRoundTrip).

Root cause: the contract did not say whether a finished action's payload must stay readable, and the builder cannot see the hidden tests to learn it.

Evidence: .agent/logs/judge-F-064.md rounds 5-6; caller's session report.

Prevention: state retention and readability of terminal-state data in the contract; if a behaviour change touches stored data, ask via /test-change-request instead of guessing.

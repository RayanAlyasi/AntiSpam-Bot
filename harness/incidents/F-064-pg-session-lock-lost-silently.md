# F-064/F-065 (WP-H Postgres): executor session advisory lock lost silently

Symptom: judge round 4 FAIL (major). The executor lock was a session advisory lock on one pooled connection and nothing re-checked it. If that connection died (Postgres restart, pg_terminate_backend, idle_session_timeout, NAT/LB dropping an idle TCP connection) the lock was released while the executor kept running; a second process could Lock, Recover the first one's `running` rows and run them twice, and two executors paced one number.

Root cause: a session-scoped lock was treated as a permanent fact once taken. Its lifetime is the connection's, and the lock connection is idle, so it is the one most likely to be dropped unnoticed. MemStore could not show this, so the in-memory tests passed.

Evidence: .agent/logs/judge-F-064.md (round 4 report, probe TestJudgePGLockLostWhenConnectionDies in .agent/scratch/F-064/judge5_integration_test.go). Fix: Store.HoldsLock plus a dedicated lock connection (round 5 PASS). Rounds 5 and 6 found follow-ups of the same kind: Run's `ownsLock()` read a stale in-memory flag before recoverRunning (could reset a row another executor was running), and Recover after a failed settle write can duplicate a send (at-least-once, not documented).

Prevention: for any lease or lock in a new store, the contract should state "holder re-verifies before each side effect", and the integration suite should include a kill-the-backend test. Verify with the store, not with a cached flag.

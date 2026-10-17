# Proposal: re-check every precondition after every wait in shared-resource code

Rule: In code that waits on a shared lock or limiter (call lock, rate bucket, window), re-check deadline, kill switch, breaker and pause after each wait, apply any response-derived state before releasing the lock, and yield on every shared limiter, because a check done before a wait is stale after it.

File: .claude/rules for Go concurrency/executor code (or the architect's contract checklist for queue/limiter features).
Incident: harness/incidents/F-064-judge-concurrency-and-fairness-majors.md

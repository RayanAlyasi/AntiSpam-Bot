# WP-I: re-ban after a dead kick did nothing, then a fixed retry cap stranded it (F-077)

Symptom: judge FAIL round 1: Ban on a member whose earlier kick action was failed/expired/cancelled hit the dedupe key and enqueued nothing. Round 2: the fix used a fixed maxKickRetries, so a ban with more dead kicks than the cap was still stranded.
Root cause: dedupe-by-key treated any existing row, dead or alive, as "already kicked". Same family as the F-064 dead ends (retrying against terminal actions).
Evidence: .agent/logs/judge-F-077.md; kicker.go, TestReBanAfterADeadKickKicksAgain, TestQueueKickerStopsWithoutProgress; commits 5eed1ef, 809eddc.
Prevention: dedupe must only suppress live rows (pending/running/done). Derive retry keys from the dead row id so the chain ends by monotonic IDs, not a magic cap. Test N rounds, not one.

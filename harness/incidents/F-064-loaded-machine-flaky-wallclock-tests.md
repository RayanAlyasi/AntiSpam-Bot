# F-064/F-065 (WP-H Postgres): load average ~18 made other packages' wall-clock tests flaky

Symptom: with local load average about 18, wall-clock tests in unrelated packages failed intermittently during the gate. Separately, internal/wa/adapter TestChannelsInvitesPollsLocationsEventsCommerce fails only locally with `-tags integration` (text order) while staging CI is green (judge round 6, out of scope, not reproduced here).

Root cause: parallel sessions plus Docker share one machine, and tests asserting on elapsed real time are sensitive to scheduling.

Evidence: caller report; .agent/logs/gates.jsonl; .agent/logs/judge-F-064.md round 6 UNKNOWN item.

Prevention: rerun a failing unrelated package alone before treating it as a regression; new tests use fake clocks or synctest, or growth-based checks as in #25.

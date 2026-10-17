# WP-F: judge majors that were real bugs

- Symptom: majors were (1) missing EnableDecryptedEventBuffer, so unacked messages would be dropped; (2) ban events on a droppable queue; (3) no disconnect report for manager drops; (4) bot number in alert log keys; (5) inbox dedupe ignored the sender although IDs are client-chosen; (6) hosted JIDs not canonical in the worker; (7) F-033 claimed without a writer.
- Root cause: delivery guarantees (1-3), identifier trust and privacy (4, 5), canonical forms at boundaries (6) and producer existence (7) were not checked against the contract before judging.
- Evidence: .agent/logs/judge-F-020.md, judge-F-013.md, judge-F-014.md; .agent/scratch/F-020/, .agent/scratch/F-014/.
- Prevention: a pre-judge checklist (see proposal WP-F-pre-judge-reliability-checklist).

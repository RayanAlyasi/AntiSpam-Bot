# F-106: resumed-judge stamp arrived late or did not stick

- Symptom: the stamp sometimes arrived late. Once it did not stick because the gate ran red during its hook run (see the wall-clock flake incident).
- Root cause: the stamp is written only if the gate passes at hook time, so a flaky gate drops a valid PASS.
- Evidence: .agent/logs/judge.jsonl, .agent/logs/gates.jsonl.
- Prevention: remove flaky tests (see the wall-clock incident), and re-run the judge or stamp step after a green gate rather than assuming the stamp landed.

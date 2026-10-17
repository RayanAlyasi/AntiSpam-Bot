# F-106: wall-clock bound tests in wp-a normalize flaked under parallel load

- Symptom: stamp-hook gates went red several times with no code change. Load average was around 15-18 from parallel sessions.
- Root cause: tests asserted absolute time bounds, which depend on machine load.
- Evidence: .agent/logs/gates.jsonl, .agent/logs/check-test.log. Partly fixed in #25 (growth ratio); some tests can still flake.
- Prevention: assert complexity by growth ratio or operation counts, never by wall clock. Audit the remaining timing tests.

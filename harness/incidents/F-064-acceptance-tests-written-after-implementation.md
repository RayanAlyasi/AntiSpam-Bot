# F-064 bundle: architect acceptance tests never failed against stubs

Symptom: the architect's hidden tests were only run after the implementation already existed, so there was no red phase. Nobody saw them fail against stubs, so it is unproven that they can detect a missing or wrong behaviour. Meanwhile the judge found 11+ real defects over 3 rounds (see F-064-judge-concurrency-and-fairness-majors.md) that the tests did not catch.

Root cause: ordering. The workflow did not require running the hidden suite against stubs/empty implementations before the builder starts, so test strength is unverified.

Evidence: session timeline reported by the caller; judge round 1-3 findings were all found by judge-written probes, not by the hidden tests (.agent/logs/judge-F-064.md).

Prevention: after the architect writes the tests and before implementation, run them against compile-only stubs and record that every test fails for the right reason (or a mutation sample fails them). Treat a test that passes against a stub as a defect.

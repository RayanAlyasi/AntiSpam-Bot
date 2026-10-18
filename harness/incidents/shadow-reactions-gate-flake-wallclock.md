# shadow-reactions: make check flaked twice on wall-clock tests

Symptom: make check failed twice on TestExtractBounded and TestTextBounded (pre-existing wall-clock linearity tests), unrelated to this feature.

Root cause: host load around 35 on 10 cores, with other sessions' gates running concurrently; timing-ratio assertions are sensitive to contention.

Evidence: .agent/logs/ gate runs (two failures, then green on rerun).

Prevention: not fixable by the builder (committed tests). Follow-up for a human: make those tests load-tolerant (CPU-time or op-count based), or serialize gates across worktrees. Rerunning when the failing test is untouched by the diff was the right call.

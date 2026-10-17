# WP-I: detect.Analyzer cannot carry content policy

Symptom: content policy could not be built as a detect.Analyzer, so policy.Engine had to be built separately.
Root cause: Analyzer.Analyze receives no group settings or member standing, so per-group policy can't be evaluated there.
Evidence: harness/deviations/F-053.md, F-056.md.
Prevention: the contract or design should say which layer owns settings-dependent decisions before the work is split into features.

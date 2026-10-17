# Proposal: property generators must cover the whole input space

- Rule: "Property-test generators for decision tables must enumerate every signal subset, not only the cases the feature expects, because a rewrite valid for one subset can silently change another (e.g. auto_ban applied to `known`)."
- File: the testing rules (rules file for tests).
- Incident: harness/incidents/F-106-safety-matrix-auto-ban-known.md

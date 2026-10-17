# Proposal: no wall-clock assertions in tests

- Rule: "Tests must not assert absolute elapsed time; assert growth ratios or operation counts, because parallel sessions push load average high enough to flake timing bounds and silently drop judge stamps."
- File: the testing rules (rules file for tests).
- Incident: harness/incidents/F-106-wallclock-tests-flake-under-load.md

# Proposal: ports need a conformance suite run against fake and real adapters

- Rule (for .claude/rules/tests.md): When a package is built against a port before its implementation exists, write one conformance test suite for the port and run it on the fake and on every real adapter.
- Why: fakes accepted inputs real stores reject (ErrNoPerson, missing hash key, nil Norm), costing three follow-up adapter PRs.
- Incident: harness/incidents/WP-K-ports-first-needed-three-adapter-prs.md

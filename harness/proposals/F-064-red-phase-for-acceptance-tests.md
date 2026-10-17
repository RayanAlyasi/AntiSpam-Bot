# Proposal: run the hidden tests against stubs before implementation starts

Rule: After the architect writes the acceptance tests and before the builder starts, run them against compile-only stubs and record that each fails for the intended reason, because tests that never failed are unproven to catch anything.

File: the /next-feature command instructions (architect step) and docs/AGENTIC_ENVIRONMENT.md.
Incident: harness/incidents/F-064-acceptance-tests-written-after-implementation.md

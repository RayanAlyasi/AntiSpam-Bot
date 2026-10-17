# Proposal: format only changed paths

- Rule: Run the formatter only on paths you changed (`golangci-lint fmt <paths>`), never `make fmt` on the whole tree, because it rewrites protected hidden tests that the guard then won't let you restore.
- Belongs in: `.claude/rules` Go or workflow rules (a human should also consider excluding the hidden-test directory in the formatter config).
- Incident: `harness/incidents/F-002-fmt-rewrote-hidden-test.md`

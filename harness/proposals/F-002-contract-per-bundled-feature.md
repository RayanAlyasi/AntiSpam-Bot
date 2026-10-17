# Proposal: contract per feature in a bundle

- Rule: A feature may only be marked `passes: true` if it has its own contract (and hidden tests), even when bundled with others, because otherwise it is verified only by the builder's own tests.
- Belongs in: `docs/AGENTIC_ENVIRONMENT.md` or the `/next-feature` workflow rules.
- Incident: `harness/incidents/F-002-bundle-without-contracts.md`

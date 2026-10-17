# Proposal: list entry-point invariants for features that act on groups

- Rule (for .claude/rules/tests.md): For any feature with commands, votes or posts into a group, the contract lists who may call each entry point and which groups it may post to, and a table test covers every entry point (including late and replayed ones) with a non-admin and a protected group.
- Why: WP-K had non-admin control of review groups and posts into protected groups via settings and late votes, each found only by the judge.
- Incident: harness/incidents/WP-K-judge-round1-cross-cutting-majors.md

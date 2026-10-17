# F-002: fast-track bundle passed eight features on builder tests alone

- Symptom: F-001 to F-008 and F-099 shipped in one PR (#16). Only F-002 had a contract and hidden tests; the other eight were verified only against the builder's own tests. The judge flagged this as a process note.
- Root cause: bundling skipped the per-feature contract step, so the "tests written without seeing the implementation" guarantee did not hold for those features.
- Evidence: `.agent/logs/judge-F-002.md`; PR https://github.com/RayanAlyasi/AntiSpam-Bot/pull/16; `harness/progress/F-002.md`.
- Would have prevented: requiring a contract per feature ID before `passes: true`, or marking bundled features `passes` only after contracts and hidden tests are backfilled.

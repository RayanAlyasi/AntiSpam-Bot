# Proposal: cost tests for parsers of untrusted input

- **Rule (for `.claude/rules/tests.md`):** A parser of message content gets a worst-case cost test (long continuations, many repeated items, maximum size) besides its fuzz test, and its per-message total is bounded, not only its per-item size.
- **Why:** fuzzing finds crashes, not quadratic time; a crafted message can stall the worker.
- **From:** harness/incidents/F-024-quadratic-vcard.md

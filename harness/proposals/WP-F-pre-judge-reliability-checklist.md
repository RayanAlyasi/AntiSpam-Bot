# Proposal: pre-judge reliability checklist for ingest and queue features

- Rule: "Before requesting the judge, list which events must never drop, which IDs are client-chosen (dedupe by sender plus ID), which identifiers reach log keys, and which writer produces each stored row, because these were the recurring judge majors."
- File: the builder workflow notes in docs/AGENTIC_ENVIRONMENT.md (or the next-feature skill).
- Incident: harness/incidents/WP-F-judge-majors-ingest-and-queue.md.

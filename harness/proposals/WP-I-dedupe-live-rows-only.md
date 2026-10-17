# Proposal: dedupe keys suppress only live actions

Rule: Queue dedupe must treat failed, expired and cancelled rows as absent (derive a retry key from the dead row id), because otherwise a retry or re-ban is silently dropped. Test the repeat N times, not once.
File: docs/DESIGN.md action-queue section (and the Go rules file for queue consumers).
Incident: harness/incidents/WP-I-reban-after-dead-kick.md (see also harness/dead-ends/F-064.md)

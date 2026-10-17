# Proposal: don't change signatures that committed tests call

Rule: When a committed test calls a function, add a new function or option instead of changing its signature, because the guard forbids editing (or even restoring) that test mid-session.
File: the Go coding rules or the /next-feature instructions.
Incident: harness/incidents/WP-I-edited-committed-test-callsites.md

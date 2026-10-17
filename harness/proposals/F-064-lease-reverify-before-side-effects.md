# Proposal: re-verify leases against the store before side effects

Rule: any lock or lease that gates an external side effect (WhatsApp call, send) must be re-verified against the backing store, not a cached flag, before each effect, and the integration suite must include a test that kills the holder's connection.

Reason: a Postgres session advisory lock dies silently with its connection, which allowed two executors; cached in-memory flags were stale too.

File: the contract/judge checklist in docs/AGENTIC_ENVIRONMENT.md (or the store-backed rules under .claude/rules; human to choose).

Incident: harness/incidents/F-064-pg-session-lock-lost-silently.md

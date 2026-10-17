# Proposal: tamper audit compares against origin, not local staging

Rule: The tamper audit must diff against a freshly fetched `origin/staging`, because the local `staging` ref is pinned by the parent checkout and goes stale, giving false failures.
File: the tamper script behind the Makefile (human-owned).
Incident: harness/incidents/WP-I-harness-friction-merge-tamper-guard.md

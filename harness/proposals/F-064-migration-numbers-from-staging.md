# Proposal: migration numbers come from origin/staging, contiguous

Rule: name a new migration with the next contiguous number after the highest on origin/staging (never the feature ID), and re-check after every staging merge.

Reason: parallel branches picked the same number (00004), and a feature-ID number (00064) broke the committed contiguity test.

File: the migrations rules under .claude/rules (or CLAUDE.md "Working style").

Incident: harness/incidents/F-064-migration-number-collision-after-staging-merge.md

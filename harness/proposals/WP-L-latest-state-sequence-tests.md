# Proposal: stores where the latest state wins get sequence tests

Rule: any store resolving "the current entry" must be tested over sequences of confirm, retract and overturn (including repeats), not single transitions.

Reason: confirm, confirm, overturn left spam known because only one live row was retracted.

File: `.claude/rules` testing rules.

Incident: harness/incidents/F-117-judge-round2-entryfor-overturn.md

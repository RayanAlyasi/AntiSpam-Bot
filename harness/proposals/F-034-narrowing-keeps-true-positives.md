# Proposal: narrowing a detector must re-test its true positives

Rule: When a change reduces false positives in a detector, add case and shape variants of known true positives (for example "Evil.Com") to the same table test, because a broadened rejection rule silently disables detection of CERTAIN-band sources.

File: the testing rules (builder checklist).
Incident: harness/incidents/F-034-title-case-tld-regression.md

# Proposal: explain the whole run when searching sub-spans

- Rule: When a detector searches sub-spans of a longer token run, accept a hit only if every remaining part of the run is explained as a known benign shape, and add negative tests for IBAN/card/account/date shapes first, because sub-span search turns interior groups of structured numbers into false positives.
- File: the detection rules file for internal/detect/ (e.g. .claude/rules/detection.md).
- Incident: harness/incidents/contact-phone-shapes-spaced-iban-subrun.md

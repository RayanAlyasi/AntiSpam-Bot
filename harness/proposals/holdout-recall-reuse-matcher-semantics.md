# Proposal: check a reused matcher's meaning before using it as a detection signal

Rule: when a detector reuses another package's matcher (for example redact), test it against benign text with dates, times and ID lists first, because a matcher built to hide data matches far more than the thing you want to detect.

Belongs in: the detector rules file for internal/detect (.claude/rules).

Source incident: harness/incidents/holdout-recall-contact-trusted-redact.md

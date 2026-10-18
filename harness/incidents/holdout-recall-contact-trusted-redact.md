# holdout-recall: contact analyzer trusted redact.Text, so dates counted as phone numbers

Symptom: judge FAIL round 1. Dates, times, year lists and 9-digit IDs counted as phone numbers, so related ordinary chat containing a date reached admins as a campaign ad.

Root cause: the contact analyzer treated every span redact.Text masks as a phone number. redact masks any run of 9+ digits because it is built to hide data, not to identify numbers.

Evidence: .agent/scratch/holdout-recall/judge-first-review.md, .agent/logs/judge-holdout-recall.md (re-review, finding RESOLVED), commit c595b99 (internal/detect/contact), PR #65.

Prevention: before using another package's matcher as a detection signal, run it on benign text with dates, times, year lists and IDs. Add a benign "wording plus date" table case up front.

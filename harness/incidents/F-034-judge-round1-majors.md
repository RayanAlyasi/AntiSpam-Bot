# F-034 bundle: judge round 1 FAIL with 6 majors

Symptom: judge FAIL on first review. Majors: Norm not idempotent (tatweel joins mark runs), ParsePhone rejects wa.me forms, spaced invite chain host, prose sentence breaks becoming URLs, ellipsis-glued links, phone parse budget evasion.

Root cause: the builder's tests were happy-path table tests plus a few fuzz seeds. Contract phrases like "any of the forms above" and "idempotent" were checked against examples, not against every listed form or an adversarial pairing of normalization steps (FoldArabic removes tatweel after flood counting, so marks on both sides merge uncounted). Budgets (phone parse) were tested only with plain-digit noise.

Evidence: .agent/logs/judge-F-034.md (round 1); probes in .agent/scratch/F-034/probe*_test.go; commits 39daac1, daf1b23.

Prevention: for each contract sentence with "any of" or "idempotent", enumerate the forms into a table test before coding; run Norm(Norm(x)) == Norm(x) as a fuzz property with step-interaction seeds (a step that deletes characters can join runs counted by an earlier step); test every resource budget with adversarial noise of each start-marker kind.

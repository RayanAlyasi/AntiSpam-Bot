# WP-I: judge FAILs on decision semantics (F-059, F-076)

Symptom: judge round 1 failed the first PR (#15) and the second PR: CERTAIN+ai counted as spam (decide.go isSpam); a compromised member could be banned when compromised.enabled was false (blocker); kill switches left the old score and band, so a killed signal still drove the outcome (F-076).
Root cause: the contract's band/source rules and the "compromised never banned" guarantee (DESIGN 8.7) were implemented as separate branches, and the property tests generated settings without the off-switches (genSettings did not draw Compromised.Enabled or Ladder.Enabled). Kill switches were applied to the output instead of to the log-odds input.
Evidence: .agent/logs/judge-F-059.md, .agent/logs/judge-F-076.md; harness/deviations/F-059.md, F-076.md; fixes in 668df70 / 2ff38eb.
Prevention: property-test generators must draw every boolean setting that can disable a safety path, and assert the never-ban invariant across all of them. Re-derive a decision from the remaining evidence when an input is disabled; never patch the result.

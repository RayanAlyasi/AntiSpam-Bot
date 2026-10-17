# F-034 bundle: round 2 FAIL, prose fix regressed bare-domain detection

Symptom: round 2 judge major: "Evil.Com", "Evil.Net", "Evil.Xyz" extracted nothing, so one capital letter defeated the domain blocklist.

Root cause: to fix round-1 prose-sentence-break false URLs, `titleCaseTLD` rejection was applied to every suffix instead of only word-like TLDs (Me, Shop, Live, Info, Co). The fix was validated against the false-positive examples only, not against previously passing true-positive forms.

Evidence: .agent/logs/judge-F-034.md round 2 (extract.go:316-338, probe5_test.go TestProbeTitleCaseBareDomain); resolved in round 3 via `titleCaseWordTLD`.

Prevention: when narrowing a detector to cut false positives, add the nearest true-positive variants (case changes of known-good inputs) to the same table test before the change.

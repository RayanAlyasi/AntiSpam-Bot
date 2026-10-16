---
name: harness-report
description: Shows whether the build system is getting better or worse, from the per-feature scorecards. Use when the user asks how the agents are doing, or before and after any harness change.
disable-model-invocation: true
---
Run `python3 harness/scripts/report.py`. Present the result as:
1. the overall trend: rolling 10-feature windows of the composite score, per harness version;
2. per category;
3. any alarms (score drop > 10%, any tamper hit, judge fail rate > 50% over 5 features, escaped bugs);
4. the three weakest metrics, with the features that drove them.

Don't change anything; this skill only reports.

# WP-G: five judge passes, three of them only for merge resolutions

- Symptom: the judge ran five times: round 1 (FAIL), round 2 (PASS), then three merge-only re-reviews (staging 2cb04b0, 8479285, 1d277bb). Staging moved again (#21, #33, #34, #35) while reviews ran; one merge review noted "staging has moved on to 32d206b since this merge started".
- Root cause: a merge commit needs its own stamp, and the branch lived long enough for staging to move several times, with merges interleaved with work.
- Evidence: .agent/logs/judge-WP-G.md, .agent/logs/judge-WP-G-merge.md; git log main..HEAD (six "Merge remote-tracking branch 'origin/staging'" commits).
- Would have prevented: merging staging once, right before the final judge run (existing proposal F-034-merge-staging-before-judge); a shorter-lived, smaller bundle.

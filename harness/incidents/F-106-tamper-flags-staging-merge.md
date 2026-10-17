# F-106: conflicted staging merge makes tamper flag staging's own test edits

- Symptom: the stamp hook's gate could never pass on a branch with an uncommitted staging merge.
- Root cause: tamper diffs against the local `staging` ref. An uncommitted merge leaves staging's test edits in the diff, so they look like agent edits. It recurs whenever a PR builds on a squash-merged parent PR.
- Evidence: .agent/logs/check-tamper.log, .agent/logs/gates.jsonl.
- Workaround: re-create the branch on staging and `git apply` the PR diff instead of merging.
- Prevention: tamper should compare against the merge-base, or ignore paths changed on staging since it. Alternatively document the re-create workflow in /next-feature.

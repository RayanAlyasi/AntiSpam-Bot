# contact-phone-shapes: branch based on a squash-merged feature branch

- Symptom: a plain `git rebase origin/staging` replayed #65's original commits, which duplicated the squash commit already in staging.
- Root cause: the branch was started from feat/holdout-recall before #65 merged. A squash-merge leaves the original commits unrecognised by rebase.
- Evidence: `git log main..HEAD`; the fix was `git rebase --onto origin/staging OLD_65_TIP`.
- Prevention: when a branch is stacked on an unmerged feature branch, record the parent tip SHA in the progress notes. After the parent is squash-merged, use `rebase --onto`.

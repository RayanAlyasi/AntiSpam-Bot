---
name: harness-eval
description: A/B-tests one harness change (CLAUDE.md, rules, a skill, an agent or a hook) by replaying the eval tasks before and after it. Human-started only.
disable-model-invocation: true
---
Requires `harness/evals/` to hold at least 20 tasks; below that, results can't be told apart from noise. Each task is a directory with `base` (a commit), `prompt.md`, and `grade/` (hidden grading tests).

1. Confirm with the user which single change is under test: arm B is a commit or patch, arm A is `main`.
2. For each task and each arm, at least 3 runs: `harness/scripts/eval-run.sh <task> <arm>`. It creates a clean worktree at `base`, runs `claude -p` with the task prompt, then runs the grading tests and records pass/fail, cost and turns to `harness/evals/results/`.
3. Compare the arms:
   - regression tasks must stay at 100%;
   - capability pass rate must not drop;
   - cost must not rise by more than 15%.
4. Report the table and a recommendation. Record the outcome in `harness/CHANGELOG.md`. Merge nothing yourself.

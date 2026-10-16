#!/bin/bash
# Replay one eval task on one harness arm. Usage: eval-run.sh <task-dir-name> <arm-ref> [run-number]
# A task dir in harness/evals/ holds: base (a commit SHA), prompt.md, grade/ (hidden grading tests,
# copied in only after the agent finishes). Results append to harness/evals/results/results.jsonl.
set -u
root=$(git rev-parse --show-toplevel)
task="$1"; arm="$2"; n="${3:-1}"
tdir="$root/harness/evals/$task"
[ -f "$tdir/base" ] && [ -f "$tdir/prompt.md" ] && [ -d "$tdir/grade" ] || { echo "bad task: $task"; exit 1; }
wt=$(mktemp -d "/tmp/harness-eval-XXXX")
git -C "$root" worktree add -q --detach "$wt" "$(cat "$tdir/base")"
# Apply the harness arm: take .claude/, CLAUDE.md and harness/scripts from the arm ref.
git -C "$wt" checkout -q "$arm" -- .claude CLAUDE.md harness/scripts 2>/dev/null
(cd "$wt" && claude -p "$(cat "$tdir/prompt.md")" --permission-mode dontAsk --max-turns 150 \
    --output-format json >"$wt/.eval-out.json" 2>/dev/null)
mkdir -p "$wt/acceptance/eval_grade" && cp -R "$tdir/grade/." "$wt/acceptance/eval_grade/"
(cd "$wt" && TZ=UTC go test ./acceptance/eval_grade/... >"$wt/.grade.log" 2>&1); pass=$?
cost=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get("total_cost_usd") or 0)' "$wt/.eval-out.json" 2>/dev/null || echo 0)
mkdir -p "$root/harness/evals/results"
printf '{"task":"%s","arm":"%s","run":%s,"pass":%s,"cost_usd":%s}\n' "$task" "$arm" "$n" \
  "$([ $pass -eq 0 ] && echo true || echo false)" "$cost" >>"$root/harness/evals/results/results.jsonl"
git -C "$root" worktree remove --force "$wt"

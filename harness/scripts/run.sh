#!/bin/bash
# Stage B: unattended, one feature per fresh session, until something needs a human.
# Run inside a container or with the Claude Code sandbox enabled (docs/AGENTIC_ENVIRONMENT.md §5.3).
# Requires: the `claude` CLI, a trusted project folder (so project allow-rules apply in -p mode),
# and `make setup-hooks` done once.
set -u
root=$(git rev-parse --show-toplevel); cd "$root" || exit 1
max_features="${1:-1000}"
logs="$root/.agent/logs"; mkdir -p "$logs"

for ((i = 1; i <= max_features; i++)); do
  [ -f "$root/.agent/STOP" ] && { echo "STOP file present; stopping."; break; }
  git switch -q main && git pull -q --ff-only || { echo "can't update main; stopping."; break; }
  out="$logs/run-$(date +%Y%m%d-%H%M%S).json"
  echo "== session $i =="
  # Never --bare: it would skip the hooks, skills, agents and CLAUDE.md that make this safe.
  claude -p "/next-feature" --permission-mode dontAsk --max-turns 150 --output-format json >"$out" 2>"$out.err"
  status=$?
  python3 - "$out" <<'PY'
import json, sys, os
sys.path.insert(0, "harness/scripts")
import hooklib as h
try:
    d = json.load(open(sys.argv[1]))
except ValueError:
    sys.exit(0)
root = h.repo_root(".")
h.log_event(root, "cost.jsonl", {"cost_usd": d.get("total_cost_usd"), "turns": d.get("num_turns"),
                                  "duration_ms": d.get("duration_ms"), "session": d.get("session_id")})
PY
  [ $status -ne 0 ] && { echo "session exited with $status; see $out.err"; break; }
  if [ -f .agent/plan.md ] && grep -q "BLOCKED:" .agent/plan.md; then
    echo "blocked: $(grep -m1 'BLOCKED:' .agent/plan.md)"; break
  fi
  ls harness/test-change-requests/*.md >/dev/null 2>&1 && { echo "test-change request waiting for a human"; break; }
  python3 -c 'import json,sys; f=json.load(open("harness/features.json"))["features"]; sys.exit(0 if any(not x["passes"] for x in f) else 1)' \
    || { echo "all features pass"; break; }
done

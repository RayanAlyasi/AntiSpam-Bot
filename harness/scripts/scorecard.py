#!/usr/bin/env python3
"""Write one scorecard line for a finished feature to harness/metrics.jsonl.

Usage: scorecard.py F-xxx
Reads the per-branch logs in .agent/logs/ (gates, judge, guard, cost). Fields that can't be
measured yet are null, never guessed.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hooklib as h  # noqa: E402

fid = sys.argv[1] if len(sys.argv) > 1 else ""
root = h.repo_root(os.getcwd())
br = h.branch(root)
fid = fid or h.feature_id(root)
logs = os.path.join(root, ".agent", "logs")


def records(name):
    path = os.path.join(logs, name)
    if not os.path.exists(path):
        return []
    out = []
    for line in open(path):
        try:
            r = json.loads(line)
        except ValueError:
            continue
        if r.get("branch") == br or r.get("feature") == fid:
            out.append(r)
    return out


gates = records("gates.jsonl")
judge = records("judge.jsonl")
guard = records("guard.jsonl")
cost = records("cost.jsonl")

category, core = None, None
try:
    for f in json.load(open(os.path.join(root, "harness", "features.json"))).get("features", []):
        if f.get("id") == fid:
            category, core = f.get("category"), f.get("core")
except (OSError, ValueError):
    pass

card = {
    "feature": fid,
    "branch": br,
    "category": category,
    "core": core,
    "harness_version": h.harness_version(root),
    "commit": h.run(["git", "rev-parse", "--short", "HEAD"], root).stdout.strip(),
    "gate_runs": len(gates),
    "gate_first_pass": gates[0]["ok"] if gates else None,
    "gate_failed_steps": sorted({g.get("failed_step") for g in gates if g.get("failed_step")}),
    "acceptance_first_pass": None if not gates else not any(g.get("failed_step") == "test" for g in gates[:1]),
    "judge_rounds": len(judge),
    "judge_first_verdict": judge[0]["verdict"] if judge else None,
    "judge_final_verdict": judge[-1]["verdict"] if judge else None,
    "tamper_hits": sum(1 for g in gates if g.get("failed_step") == "tamper"),
    "guard_blocks": len(guard),
    "test_change_requests": len([f for f in os.listdir(os.path.join(root, "harness", "test-change-requests"))
                                 if f.startswith(fid)]) if fid else 0,
    "cost_usd": round(sum(c.get("cost_usd") or 0 for c in cost), 4) if cost else None,
    "turns": sum(c.get("turns") or 0 for c in cost) if cost else None,
    "mutation_score": None,  # filled by the nightly job once core packages exist
    "human_verdict": None,   # filled by report.py from the PR state
}
card["ts"] = h.run(["date", "-u", "+%Y-%m-%dT%H:%M:%SZ"], root).stdout.strip()
with open(os.path.join(root, "harness", "metrics.jsonl"), "a") as f:
    f.write(json.dumps(card) + "\n")
print(json.dumps(card, indent=1))

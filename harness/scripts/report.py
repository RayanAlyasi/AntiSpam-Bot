#!/usr/bin/env python3
"""Is the build system getting better or worse? Rolling composite scores per harness version
and category, plus alarms. Reads harness/metrics/*.json and harness/scoring.json.
Human verdicts come from the PR state via `gh` when it's available.
"""
import json
import os
import subprocess
import sys
from collections import defaultdict

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hooklib as h  # noqa: E402

root = h.repo_root(os.getcwd())
cfg = json.load(open(os.path.join(root, "harness", "scoring.json")))
W, window = cfg["weights"], cfg["window"]
cards = []
mdir = os.path.join(root, "harness", "metrics")
if os.path.isdir(mdir):
    for fn in os.listdir(mdir):
        if fn.endswith(".json"):
            cards.append(json.load(open(os.path.join(mdir, fn))))
legacy = os.path.join(root, "harness", "metrics.jsonl")
if os.path.exists(legacy):
    cards += [json.loads(l) for l in open(legacy) if l.strip()]
cards.sort(key=lambda c: c.get("ts") or "")
if not cards:
    print("No scorecards yet. They're written at the end of each /next-feature run.")
    sys.exit(0)


def pr_verdict(branch):
    try:
        out = subprocess.run(["gh", "pr", "view", branch, "--json", "state,reviewDecision"],
                             cwd=root, capture_output=True, text=True, timeout=20)
        if out.returncode != 0:
            return None
        d = json.loads(out.stdout)
        if d.get("state") == "MERGED":
            return 0.6 if d.get("reviewDecision") == "CHANGES_REQUESTED" else 1.0
        if d.get("state") == "CLOSED":
            return 0.0
    except (OSError, ValueError, subprocess.TimeoutExpired):
        pass
    return None


costs = sorted(c["cost_usd"] for c in cards if c.get("cost_usd"))
median_cost = costs[len(costs) // 2] if costs else None


def composite(c):
    parts = {
        "human_verdict": pr_verdict(c["branch"]) if c.get("branch") else None,
        "judge_first_pass": None if c.get("judge_first_verdict") is None else float(c["judge_first_verdict"] == "PASS"),
        "acceptance_first_pass": None if c.get("acceptance_first_pass") is None else float(c["acceptance_first_pass"]),
        "gate_efficiency": None if not c.get("gate_runs") else min(1.0, 2.0 / c["gate_runs"]),
        "integrity": 0.0 if c.get("tamper_hits") else 1.0,
        "mutation_score": c.get("mutation_score"),
        "cost_efficiency": None if not (c.get("cost_usd") and median_cost) else min(1.0, median_cost / c["cost_usd"]),
    }
    known = {k: v for k, v in parts.items() if v is not None}
    total = sum(W[k] for k in known)
    return (sum(W[k] * v for k, v in known.items()) / total if total else None), parts


rows = []
for c in cards:
    score, parts = composite(c)
    rows.append((c, score, parts))

print(f"{len(rows)} features scored\n")
print("Rolling composite (window %d):" % window)
scores = [s for _, s, _ in rows if s is not None]
for i in range(0, len(scores), window):
    chunk = scores[i:i + window]
    print(f"  features {i + 1}-{i + len(chunk)}: {sum(chunk) / len(chunk):.3f}")

by_version, by_cat = defaultdict(list), defaultdict(list)
for c, s, _ in rows:
    if s is not None:
        by_version[c.get("harness_version")].append(s)
        by_cat[c.get("category")].append(s)
print("\nBy harness version:")
for v, ss in by_version.items():
    print(f"  {v}: {sum(ss) / len(ss):.3f} over {len(ss)} features")
print("\nBy category:")
for k, ss in sorted(by_cat.items(), key=lambda x: str(x[0])):
    print(f"  {k}: {sum(ss) / len(ss):.3f} over {len(ss)} features")

alarms = []
if len(scores) >= 2 * window:
    prev, last = scores[-2 * window:-window], scores[-window:]
    drop = (sum(prev) / window - sum(last) / window) / max(sum(prev) / window, 1e-9) * 100
    if drop > cfg["alarms"]["composite_drop_pct"]:
        alarms.append(f"composite dropped {drop:.1f}% over the last {window} features")
if any(c.get("tamper_hits") for c, _, _ in rows):
    alarms.append("tamper hits: " + ", ".join(c["feature"] for c, _, _ in rows if c.get("tamper_hits")))
last5 = [c for c, _, _ in rows[-5:]]
if len(last5) == 5:
    fails = sum(1 for c in last5 if c.get("judge_first_verdict") == "FAIL") / 5
    if fails > cfg["alarms"]["judge_fail_rate_over_5"]:
        alarms.append(f"judge first-round fail rate {fails:.0%} over the last 5 features")
print("\nAlarms: " + ("; ".join(alarms) if alarms else "none"))

print("\nWeakest components (mean over all features):")
comp = defaultdict(list)
for _, _, parts in rows:
    for k, v in parts.items():
        if v is not None:
            comp[k].append(v)
for k, vs in sorted(comp.items(), key=lambda x: sum(x[1]) / len(x[1]))[:3]:
    print(f"  {k}: {sum(vs) / len(vs):.2f}")

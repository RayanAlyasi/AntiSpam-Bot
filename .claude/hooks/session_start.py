#!/usr/bin/env python3
"""SessionStart (startup, compact): print a short state summary; stdout becomes context."""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402

data = h.read_input()
root = h.repo_root(data.get("cwd"))
out = [f"Harness state (branch {h.branch(root) or '?'}, harness version {h.harness_version(root)}):"]

plan = os.path.join(root, ".agent", "plan.md")
if os.path.exists(plan):
    lines = open(plan).read().splitlines()
    blocked = [l for l in lines[:5] if "BLOCKED:" in l]
    todo = [l.strip() for l in lines if l.strip().startswith("- [ ]")]
    out.append(f"- Active feature checklist: .agent/plan.md ({len(todo)} open)" + (f"; {blocked[0].strip()}" if blocked else ""))
    if todo:
        out.append(f"  next: {todo[0][6:][:120]}")
try:
    feats = json.load(open(os.path.join(root, "harness", "features.json"))).get("features", [])
    done = sum(1 for f in feats if f.get("passes"))
    out.append(f"- Features: {done}/{len(feats)} passing")
except (OSError, ValueError):
    out.append("- Features: harness/features.json not initialised yet (run the initializer, harness/INITIALIZER.md)")

tcr = os.path.join(root, "harness", "test-change-requests")
if os.path.isdir(tcr) and [f for f in os.listdir(tcr) if f.endswith(".md")]:
    out.append(f"- Open test-change requests: {len([f for f in os.listdir(tcr) if f.endswith('.md')])} (a human must resolve them)")

dead = os.path.join(root, "harness", "dead-ends")
if os.path.isdir(dead):
    entries = []
    for fn in sorted(os.listdir(dead)):
        if fn.endswith(".md") and fn != "README.md":
            entries += [l for l in open(os.path.join(dead, fn)).read().splitlines() if l.startswith("- ")]
    if entries:
        out.append("- Recent dead ends: " + " | ".join(e[2:][:100] for e in entries[-3:]))
print("\n".join(out))

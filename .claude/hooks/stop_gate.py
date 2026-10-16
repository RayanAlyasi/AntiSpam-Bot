#!/usr/bin/env python3
"""Stop hook: keep working while Go changes fail the gate or the feature checklist has open items.

Exit 2 sends the reason back to Claude and continues the turn. Both loops are capped so a stuck
session ends instead of spinning.
"""
import os
import subprocess
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402

GATE_CAP = 5
CHECKLIST_CAP = 3

data = h.read_input()
root = h.repo_root(data.get("cwd"))
session = data.get("session_id") or "unknown"
logs = os.path.join(root, ".agent", "logs")
os.makedirs(logs, exist_ok=True)


def bump(kind):
    path = os.path.join(logs, f"stop-{kind}-{session}.count")
    n = int(open(path).read() or 0) + 1 if os.path.exists(path) else 1
    with open(path, "w") as f:
        f.write(str(n))
    return n


def reset(kind):
    path = os.path.join(logs, f"stop-{kind}-{session}.count")
    if os.path.exists(path):
        os.remove(path)


changed = h.run(["git", "status", "--porcelain", "--", "*.go", "go.mod", "go.sum", "*.sql"], root).stdout.strip()
if changed and os.path.exists(os.path.join(root, "go.mod")):
    gate = subprocess.run(["make", "check-fast"], cwd=root, capture_output=True, text=True)
    if gate.returncode != 0:
        if bump("gate") <= GATE_CAP:
            out = (gate.stdout + gate.stderr).strip().splitlines()
            sys.stderr.write("make check-fast is red. Fix it before stopping, or write BLOCKED: <reason> "
                             "at the top of .agent/plan.md.\n" + "\n".join(out[-40:]) + "\n")
            sys.exit(2)
    else:
        reset("gate")

plan = os.path.join(root, ".agent", "plan.md")
if os.path.exists(plan):
    text = open(plan).read()
    if "BLOCKED:" in text[:2000]:
        sys.exit(0)
    open_items = [l.strip() for l in text.splitlines() if l.strip().startswith("- [ ]")]
    if open_items and bump("checklist") <= CHECKLIST_CAP:
        sys.stderr.write("The feature checklist in .agent/plan.md still has open items:\n"
                         + "\n".join(open_items[:5])
                         + "\nContinue with the next item. If you can't, write BLOCKED: <reason> at the top of the file.\n")
        sys.exit(2)
sys.exit(0)

#!/usr/bin/env python3
"""SubagentStop hook for the judge: a PASS plus a green gate writes the stamp for the staged diff.

The hook writes the stamp, not the model, so a stamp can't be produced by asking for one.
"""
import glob
import os
import subprocess
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402

data = h.read_input()
root = h.repo_root(data.get("cwd"))
msg = (data.get("last_assistant_message") or "").strip()
verdict = msg.splitlines()[0].strip().upper() if msg else ""
feature = h.feature_id(root)

stamps = os.path.join(root, ".agent", "stamps")
os.makedirs(stamps, exist_ok=True)
for old in glob.glob(os.path.join(stamps, "*")):
    os.remove(old)

record = {"feature": feature, "verdict": verdict or "NONE", "stamped": False}
if verdict == "PASS":
    gate = subprocess.run(["make", "check-fast"], cwd=root, capture_output=True, text=True)
    if gate.returncode == 0:
        digest = h.staged_hash(root)
        with open(os.path.join(stamps, digest), "w") as f:
            f.write(feature + "\n")
        record.update(stamped=True, diff=digest[:12])
    else:
        record["gate"] = "red"
h.log_event(root, "judge.jsonl", record)
sys.exit(0)

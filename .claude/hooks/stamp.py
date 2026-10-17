#!/usr/bin/env python3
"""SubagentStop hook for the judge: a PASS plus a green gate writes the stamp for the staged diff.

The hook writes the stamp, not the model, so a stamp can't be produced by asking for one.
"""
import glob
import json
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402


def handback_from_transcript(path, attempts=10, delay=0.3):
    """The judge's final report, read from its transcript.

    Subagents can return their report through a SubagentHandback tool call, in which case
    last_assistant_message is empty. The transcript is written asynchronously, so retry briefly.
    Returns the last handback message, or failing that the last assistant text.
    """
    if not path:
        return ""
    for _ in range(attempts):
        handback, text = "", ""
        if os.path.exists(path):
            for line in open(path, errors="replace"):
                try:
                    entry = json.loads(line)
                except ValueError:
                    continue
                msg = entry.get("message") or {}
                if msg.get("role") != "assistant" or not isinstance(msg.get("content"), list):
                    continue
                for part in msg["content"]:
                    if part.get("type") == "tool_use" and part.get("name") == "SubagentHandback":
                        handback = (part.get("input") or {}).get("message") or handback
                    elif part.get("type") == "text" and part.get("text", "").strip():
                        text = part["text"]
        if handback or text:
            return handback or text
        time.sleep(delay)
    return ""


def verdict_of(report):
    for line in report.splitlines():
        line = line.strip().strip("*#` ").upper()
        if line:
            return line.split()[0] if line.split() else ""
    return ""


data = h.read_input()
root = h.repo_root(data.get("cwd"))
report = (data.get("last_assistant_message") or "").strip()
source = "last_assistant_message"
if verdict_of(report) not in ("PASS", "FAIL", "UNKNOWN"):
    report = handback_from_transcript(data.get("agent_transcript_path"))
    source = "transcript"
verdict = verdict_of(report)
feature = h.feature_id(root)

stamps = os.path.join(root, ".agent", "stamps")
os.makedirs(stamps, exist_ok=True)
for old in glob.glob(os.path.join(stamps, "*")):
    os.remove(old)

record = {"feature": feature, "verdict": verdict or "NONE", "source": source, "stamped": False}
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

#!/usr/bin/env python3
"""SessionEnd: record that a session ended, for the scorecard. Must finish fast."""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402

data = h.read_input()
root = h.repo_root(data.get("cwd"))
h.log_event(root, "sessions.jsonl", {
    "session": data.get("session_id"), "reason": data.get("reason"),
    "feature": h.feature_id(root), "harness_version": h.harness_version(root),
})

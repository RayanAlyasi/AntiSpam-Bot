#!/usr/bin/env python3
"""Tamper audit: fails when a change weakens the checks instead of fixing the code.

Compares the working tree (staged and unstaged) with the merge-base of main.
"""
import json
import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import hooklib as h  # noqa: E402

root = h.repo_root(os.getcwd())
base = h.run(["git", "merge-base", "HEAD", "main"], root).stdout.strip() or "HEAD"
problems = []


def diff(*paths):
    return h.run(["git", "diff", "-U0", base, "--", *paths], root).stdout


def added_files():
    out = h.run(["git", "diff", "--name-only", "--diff-filter=A", base], root).stdout.split()
    out += h.run(["git", "ls-files", "--others", "--exclude-standard"], root).stdout.split()
    return set(out)


new_files = added_files()
current = None
for line in diff("*_test.go").splitlines():
    if line.startswith("+++ b/"):
        current = line[6:]
        continue
    if current is None or current in new_files:
        continue
    if line.startswith("-") and not line.startswith("---") and line[1:].strip():
        problems.append(f"{current}: line removed from an existing test: {line[1:].strip()[:80]}")
    if line.startswith("+") and re.search(r"\bt\.Skip(Now|f)?\(", line):
        problems.append(f"{current}: t.Skip added to an existing test")

for line in diff("*_test.go").splitlines():
    if line.startswith("+") and re.search(r"^\+//go:build\s+(?!integration\b)", line):
        problems.append("a build constraint that excludes tests was added")

for line in diff("*.go").splitlines():
    if line.startswith("+") and "//nolint" in line:
        problems.append(f"//nolint added: {line[1:].strip()[:80]}")

# features.json: only `passes` and `evidence` may change.
fj = os.path.join(root, "harness", "features.json")
if os.path.exists(fj):
    old = h.run(["git", "show", f"{base}:harness/features.json"], root)
    if old.returncode == 0:
        def strip(doc):
            for f in doc.get("features", []):
                f.pop("passes", None)
                f.pop("evidence", None)
            return doc
        try:
            before = strip(json.loads(old.stdout))
            after = strip(json.load(open(fj)))
            if before != after and before.get("features"):
                problems.append("harness/features.json changed beyond `passes` and `evidence`")
        except ValueError:
            problems.append("harness/features.json is not valid JSON")

if problems:
    print("Tamper audit failed. These weaken the checks instead of fixing the code:")
    for p in problems:
        print("  - " + p)
    print("If a test is genuinely wrong, use /test-change-request.")
    sys.exit(1)
print("tamper audit: clean")

"""Shared logic for the harness hooks (Python 3.9+, standard library only).

Policy lives here so the Claude Code hooks and the git pre-commit hook agree.
"""
import fnmatch
import hashlib
import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone

# Agents that may read the hidden acceptance tests.
ACCEPTANCE_READERS = {"architect", "judge"}

# Never readable or writable by any agent: a real WhatsApp session and personal messages.
SENSITIVE = ["auth_info_baileys/", "whatsapp_logs.db", ".env"]

# Writable by nobody through agent tools (hooks and humans only).
PROTECTED_WRITE = [
    "acceptance/", "testdata/fixtures/", "harness/evals/", "harness/rules-ledger.json",
    "harness/metrics.jsonl", "harness/scoring.json", "harness/scripts/", ".agent/stamps/",
    ".agent/contracts/", ".claude/", ".githooks/", "Makefile", ".golangci.yml",
    ".go-arch-lint.yml", "CLAUDE.md",
]

# Per-agent write allow-lists. The main session (empty agent type) uses PROTECTED_WRITE instead.
AGENT_WRITES = {
    "architect": ["acceptance/", ".agent/contracts/", "*_stub.go"],
    "judge": [".agent/scratch/", ".agent/logs/judge-*"],
    "retro": ["harness/incidents/", "harness/proposals/", "harness/dead-ends.md"],
    "curator": ["harness/curation/"],
    "explorer": [],
}

# Commits touching only these paths are bookkeeping and need no judge stamp.
STAMP_EXEMPT = [
    "acceptance/", ".agent/contracts/", "*_stub.go", "harness/progress.md",
    "harness/dead-ends.md", "harness/deviations.md", "harness/incidents/",
    "harness/proposals/", "harness/test-change-requests/",
]


def read_input():
    try:
        return json.load(sys.stdin)
    except ValueError:
        return {}


def run(args, cwd, check=False):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True, check=check)


def repo_root(cwd):
    r = run(["git", "rev-parse", "--show-toplevel"], cwd or ".")
    if r.returncode == 0:
        return r.stdout.strip()
    return os.environ.get("CLAUDE_PROJECT_DIR") or cwd or os.getcwd()


def branch(root):
    r = run(["git", "branch", "--show-current"], root)
    return r.stdout.strip() if r.returncode == 0 else ""


PROTECTED_BRANCHES = ("main", "master", "staging")


def base_branch(root):
    """Feature work branches from and merges into staging; fall back to main before staging exists."""
    for ref in ("staging", "origin/staging"):
        if run(["git", "rev-parse", "--verify", "-q", ref], root).returncode == 0:
            return ref
    return "main"


def feature_id(root):
    m = re.match(r"feat/(F-\d+)", branch(root))
    return m.group(1) if m else ""


def rel(path, root, cwd=None):
    if not path:
        return ""
    if not os.path.isabs(path):
        path = os.path.join(cwd or root, path)
    # Resolve symlinks on both sides (e.g. macOS /var -> /private/var), or a path given through
    # a symlink wouldn't match the repo root and would slip past every protection.
    path = os.path.realpath(path)
    root = os.path.realpath(root)
    if path == root:
        return "."
    if path.startswith(root + os.sep):
        return path[len(root) + 1:]
    return path  # outside the repo: returned absolute


def matches(relpath, patterns):
    for p in patterns:
        if p.endswith("/"):
            if relpath == p[:-1] or relpath.startswith(p):
                return True
        elif "*" in p:
            if fnmatch.fnmatch(relpath, p) or fnmatch.fnmatch(os.path.basename(relpath), p):
                return True
        elif relpath == p or relpath.startswith(p + "/") or (p == ".env" and relpath.startswith(".env")):
            return True
    return False


def is_tracked(relpath, root):
    return run(["git", "ls-files", "--error-unmatch", relpath], root).returncode == 0


def log_event(root, name, record):
    d = os.path.join(root, ".agent", "logs")
    os.makedirs(d, exist_ok=True)
    record = dict(record)
    record.setdefault("ts", datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
    record.setdefault("branch", branch(root))
    with open(os.path.join(d, name), "a") as f:
        f.write(json.dumps(record) + "\n")


def harness_version(root):
    """Hash of everything that shapes agent behaviour, so scorecards can be grouped by version."""
    h = hashlib.sha256()
    paths = ["CLAUDE.md"]
    for base in [".claude/agents", ".claude/skills", ".claude/rules", ".claude/hooks"]:
        for dirpath, _, files in os.walk(os.path.join(root, base)):
            for fn in files:
                paths.append(os.path.relpath(os.path.join(dirpath, fn), root))
    paths.append(".claude/settings.json")
    for p in sorted(paths):
        full = os.path.join(root, p)
        if os.path.isfile(full):
            h.update(p.encode())
            with open(full, "rb") as f:
                h.update(f.read())
    return h.hexdigest()[:12]


# ---- judge stamp -------------------------------------------------------------

def staged_paths(root):
    r = run(["git", "diff", "--cached", "--name-only"], root)
    return [p for p in r.stdout.splitlines() if p.strip()]


def staged_hash(root):
    r = subprocess.run(["git", "diff", "--cached", "--binary"], cwd=root, capture_output=True)
    return hashlib.sha256(r.stdout).hexdigest()


def stamp_status(root):
    """Return (ok, reason) for committing what is currently staged."""
    paths = staged_paths(root)
    if not paths:
        return True, "nothing staged"
    if all(matches(p, STAMP_EXEMPT) for p in paths):
        return True, "bookkeeping-only commit"
    stamp = os.path.join(root, ".agent", "stamps", staged_hash(root))
    if os.path.exists(stamp):
        return True, "judge stamp found"
    return False, ("No judge stamp for this exact staged diff. Stage everything (git add -A), then run "
                   "the judge agent; a PASS writes the stamp. Any change after the judge's review needs a "
                   "re-review.")

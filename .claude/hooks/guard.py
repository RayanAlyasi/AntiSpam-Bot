#!/usr/bin/env python3
"""PreToolUse guard: hidden acceptance tests, protected paths, git rules and the judge stamp.

Exit 2 with a reason on stderr blocks the tool call (Claude sees the reason).
Bash parsing is a guardrail, not a wall; the sandbox is the wall for unattended runs.
"""
import os
import re
import shlex
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "harness", "scripts"))
import hooklib as h  # noqa: E402

data = h.read_input()
tool = data.get("tool_name", "")
inp = data.get("tool_input") or {}
agent = data.get("agent_type") or ""  # empty = main session (the builder)
cwd = data.get("cwd") or os.getcwd()
root = h.repo_root(cwd)

WRITE_TOOLS = {"Edit", "Write", "MultiEdit", "NotebookEdit"}
CAN_READ_ACCEPTANCE = agent in h.ACCEPTANCE_READERS
TEST_CHANGE = " If a test or the contract looks wrong, use /test-change-request instead."


def block(reason):
    h.log_event(root, "guard.jsonl", {"agent": agent or "main", "tool": tool, "reason": reason})
    sys.stderr.write("Blocked by harness guard: " + reason + "\n")
    sys.exit(2)


def check_read(path):
    r = h.rel(path, root, cwd)
    if h.matches(r, h.SENSITIVE):
        block(f"{r} holds a real WhatsApp session or personal data and is off-limits.")
    if not CAN_READ_ACCEPTANCE and (r == "acceptance" or r.startswith("acceptance/")):
        block("acceptance/ is hidden from you. Run the tests with `make check-fast` and read their results.")


def check_write(path):
    if "$" in path or "`" in path:
        return  # unresolvable shell expression; the sandbox covers this case
    r = h.rel(path, root, cwd)
    check_read(path)
    if agent in h.AGENT_WRITES:
        if not h.matches(r, h.AGENT_WRITES[agent]):
            block(f"the {agent} agent may only write {', '.join(h.AGENT_WRITES[agent]) or 'nothing'}; not {r}.")
        if r.endswith("_test.go") and h.is_tracked(r, root):
            block(f"{r} is a committed test and can't be changed." + TEST_CHANGE)
        return
    if h.matches(r, h.PROTECTED_WRITE):
        block(f"{r} is protected (checks, fixtures, hidden tests and harness config are human-controlled)." + TEST_CHANGE)
    if r.endswith("_test.go") and h.is_tracked(r, root):
        block(f"{r} is a committed test and can't be changed. Add new tests in new files." + TEST_CHANGE)


# ---- Bash ----------------------------------------------------------------------

SEGMENT_SPLIT = re.compile(r"(\|\||&&|;|\||\n|&)")
# Commands whose every path argument is written or removed.
WRITE_ALL_ARGS = {"rm", "mv", "tee", "truncate", "chmod", "chown", "touch"}
# Commands that write only their last path argument (the destination); sources are only read.
WRITE_LAST_ARG = {"cp", "ln", "install", "rsync"}
GREP_CMDS = {"grep", "egrep", "fgrep"}
RECURSIVE_SEARCH_CMDS = {"rg", "ag", "ack"}
TREE_CMDS = {"find", "fd", "tree"}
GIT_GLOBAL_WITH_ARG = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"}


def tokens(segment):
    """Split a command segment into arguments, dropping redirections (`> file`, `2>&1`, `<in`).

    Redirection targets are checked separately; leaving them in would make them look like
    command arguments (for example a git revision).
    """
    try:
        raw = shlex.split(segment, posix=True)
    except ValueError:
        raw = segment.split()
    out, skip = [], False
    for t in raw:
        if skip:
            skip = False
            continue
        if re.fullmatch(r"\d*(>>?|<)", t) or t in ("&>", "&>>"):
            skip = True  # the next token is the redirection target
            continue
        if re.match(r"^\d*(>>?|<)|^&>", t):
            continue  # redirection with its target attached, e.g. >out.txt or 2>&1
        out.append(t)
    return out


def git_subcommand(toks):
    """Return (index_of_subcommand, subcommand, repo_dir) for a git invocation, else None."""
    if not toks or os.path.basename(toks[0]) != "git":
        return None
    i, repo = 1, None
    while i < len(toks):
        t = toks[i]
        if t in GIT_GLOBAL_WITH_ARG:
            if t == "-C" and i + 1 < len(toks):
                repo = toks[i + 1]
            i += 2
            continue
        if t.startswith("-"):
            i += 1
            continue
        return i, t, repo
    return None


def check_git(toks):
    g = git_subcommand(toks)
    if not g:
        return
    i, sub, repo = g
    args = toks[i + 1:]
    repo_dir = h.repo_root(os.path.join(cwd, repo) if repo and not os.path.isabs(repo) else (repo or cwd))

    if agent in ("judge", "explorer", "retro", "curator") and sub not in (
            "status", "diff", "log", "show", "rev-parse", "ls-files", "merge-base", "blame", "branch"):
        block(f"the {agent} agent only uses read-only git commands.")

    if sub == "commit":
        if "--no-verify" in args or "-n" in args:
            block("commits can't skip verification.")
        if "--amend" in args:
            block("amending is disabled; make a new commit (the judge stamp covers exactly one staged diff).")
        ok, reason = h.stamp_status(repo_dir)
        if not ok:
            block(reason)
    elif sub == "push":
        if any(a in ("--force", "-f", "--mirror", "--all", "--delete", "-d") for a in args):
            block("force, mirror, all-branch and delete pushes are disabled (use --force-with-lease on a feat/* branch if needed).")
        refs = [a for a in args if not a.startswith("-")][1:]  # after the remote name
        if any(re.search(r"(^|:)(refs/heads/)?(main|master|staging)$", r) for r in refs):
            block("main and staging only change through pull requests, which a human merges.")
        if (not refs or "HEAD" in refs) and not h.branch(repo_dir).startswith("feat/"):
            block("only feat/* branches are pushed. Switch to the feature branch first.")
        if refs and not all(r in ("HEAD",) or "feat/" in r for r in refs):
            block("only feat/* branches are pushed.")
    elif sub == "config" and any("hookspath" in a.lower() for a in args):
        # Reading the setting is fine; setting, adding, unsetting or editing it is not.
        writes = {"--unset", "--unset-all", "--replace-all", "--add", "--edit", "-e",
                  "--rename-section", "--remove-section"}
        values = [a for a in args if not a.startswith("-")]
        if len(values) >= 2 or writes.intersection(args):
            block("the git hooks path is part of the harness and can't be changed.")
    elif sub == "update-index" and any(a in ("--assume-unchanged", "--skip-worktree") for a in args):
        block("hiding changes from git is disabled.")
    elif sub in ("checkout", "restore", "rm", "mv", "reset", "stash", "clean"):
        for a in args:
            if not a.startswith("-") and (os.path.exists(os.path.join(cwd, a)) or "/" in a):
                check_write(a)
        if sub in ("reset", "clean") and any(a in ("--hard", "-f", "-fd", "-fdx", "-x") for a in args):
            block(f"`git {sub}` with destructive flags is disabled; it can wipe protected files and other work.")

    if not CAN_READ_ACCEPTANCE:
        if sub in ("show", "cat-file", "grep") or (sub == "log" and any(a in ("-p", "--patch", "-u") for a in args)):
            block("this git command can reveal hidden acceptance tests. Use `git log --oneline`, `git diff` or `git diff --cached`.")
        if sub == "diff":
            if "--" in args:
                paths = args[args.index("--") + 1:]
                if any(p.startswith("acceptance") for p in paths):
                    block("acceptance/ is hidden from you.")
            else:
                revs = [a for a in args if not a.startswith("-")]
                if revs:
                    block("diffs against other revisions can reveal hidden acceptance tests. Use `git diff`, `git diff --cached`, or add a pathspec: `git diff main -- internal/`.")


def covers_hidden(path):
    """True if searching `path` recursively would include acceptance/ (the repo root or acceptance itself)."""
    r = h.rel(path, root, cwd)
    return r in (".", "") or r == "acceptance" or r.startswith("acceptance/")


def check_search(prog, toks, from_pipe):
    """Block only searches that would sweep the repo (and so the hidden tests); allow targeted ones."""
    args = toks[1:]
    opts = [a for a in args if a.startswith("-")]
    plain = [a for a in args if not a.startswith("-")]
    hint = f"`{prog}` over the whole repo would include hidden tests; give it a directory such as internal/, or use the Grep tool with a path."
    if prog in GREP_CMDS:
        recursive = any(o in ("--recursive", "--dereference-recursive") or
                        (not o.startswith("--") and re.search(r"[rR]", o[1:])) for o in opts)
        if recursive:
            paths = plain[1:] if not any(o in ("-e", "-f") for o in opts) else plain
            if any(covers_hidden(p) for p in (paths or ["."])):
                block(hint)
    elif prog in RECURSIVE_SEARCH_CMDS:
        paths = plain[1:] or ([] if from_pipe else ["."])
        if any(covers_hidden(p) for p in paths):
            block(hint)
    elif prog in TREE_CMDS:
        paths = []
        for a in args:  # leading path operands, before the first option/expression
            if a.startswith("-") or a in ("(", "!"):
                break
            paths.append(a)
        if prog == "fd":
            paths = plain[1:]
        if any(covers_hidden(p) for p in (paths or ["."])):
            block(hint)


def expand(cmd):
    """Expand $VAR / ${VAR} using assignments made earlier in the command and the environment."""
    env = dict(os.environ)
    for m in re.finditer(r"(?:^|[\s;&|])([A-Za-z_][A-Za-z0-9_]*)=([^\s;&|]+)", cmd):
        env[m.group(1)] = m.group(2).strip("'\"")
    return re.sub(r"\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?", lambda m: env.get(m.group(1), m.group(0)), cmd)


def check_bash(cmd):
    cmd = expand(cmd)
    if any(s in cmd for s in ("auth_info_baileys", "whatsapp_logs.db")):
        block("that path holds a real WhatsApp session or personal data and is off-limits.")
    if re.search(r"\bgh\s+pr\s+merge\b", cmd):
        block("merging is done by a human.")
    m = re.search(r"\bgh\s+pr\s+create\b(.*)", cmd, re.S)
    if m:
        base = re.search(r"(?:--base|-B)[=\s]+['\"]?([\w./-]+)", m.group(1))
        if not base or base.group(1) != "staging":
            block("feature PRs target staging: use `gh pr create --base staging`. Release PRs from staging to main are opened by a human.")
    if re.search(r"(^|[\s/])(bash|sh|zsh)\s+-c\s", cmd):
        for m in re.finditer(r"-c\s+(['\"])(.*?)\1", cmd, re.S):
            check_bash(m.group(2))
    parts = SEGMENT_SPLIT.split(cmd)
    for idx in range(0, len(parts), 2):
        seg = parts[idx].strip()
        from_pipe = idx > 0 and parts[idx - 1] == "|"  # reads stdin, not files
        if not seg:
            continue
        toks = tokens(seg)
        if not toks:
            continue
        # strip env assignments and common wrappers
        while toks and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", toks[0]) or toks[0] in ("env", "command", "time", "nice", "xargs", "sudo")):
            toks = toks[1:]
        if not toks:
            continue
        prog = os.path.basename(toks[0])
        check_git(toks)
        if not CAN_READ_ACCEPTANCE:
            is_runner = prog == "make" or (prog == "go" and len(toks) > 1 and toks[1] in ("test", "vet", "build", "list"))
            if "acceptance" in seg and not is_runner:
                block("acceptance/ is hidden from you. `make check-fast` and `go test ./acceptance/...` show results.")
            if prog in GREP_CMDS | RECURSIVE_SEARCH_CMDS | TREE_CMDS:
                check_search(prog, toks, from_pipe)
        # writes to protected paths through the shell
        redirect_targets = re.findall(r">>?\s*([^\s;&|]+)", seg)
        for t in redirect_targets:
            if t not in ("/dev/null", "&1", "&2"):
                check_write(t)
        operands = [a for a in toks[1:] if not a.startswith("-")]
        if prog in WRITE_ALL_ARGS or (prog in ("sed", "perl") and any(a.startswith("-i") for a in toks[1:])):
            for a in operands:
                check_write(a)
        elif prog in WRITE_LAST_ARG and operands:
            check_write(operands[-1])
            for a in operands[:-1]:
                check_read(a)


if tool in WRITE_TOOLS:
    check_write(inp.get("file_path") or inp.get("notebook_path") or "")
elif tool == "Read":
    check_read(inp.get("file_path", ""))
elif tool in ("Grep", "Glob"):
    path = inp.get("path") or ""
    patt = (inp.get("pattern") or "") + " " + (inp.get("glob") or "")
    if path:
        check_read(path)
    if not CAN_READ_ACCEPTANCE:
        r = h.rel(path or cwd, root, cwd)
        if r in (".", "") or "acceptance" in patt:
            block("search a specific directory (set `path`, e.g. internal/). A repo-wide search would include hidden acceptance tests.")
elif tool == "Bash":
    check_bash(inp.get("command", ""))

sys.exit(0)

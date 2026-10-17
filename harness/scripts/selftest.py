#!/usr/bin/env python3
"""Harness self-test: proves the guardrails block what they should and allow normal work.

Run after any change to the hooks or harness scripts:  python3 harness/scripts/selftest.py
It clones this repo into a temporary folder, overlays the working-tree harness files (so
uncommitted changes are tested), and replays tool calls exactly as Claude Code sends them.
Nothing in the real repo is touched.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

SRC = subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True).stdout.strip()
TMP = tempfile.mkdtemp(prefix="harness-selftest-")
REPO = os.path.join(TMP, "repo")
fails = 0


def git(*a):
    return subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", *a],
                          cwd=REPO, capture_output=True, text=True)


def setup():
    subprocess.run(["git", "clone", "-q", SRC, REPO], check=True)
    for p in [".claude", ".githooks", "harness/scripts", "Makefile", "CLAUDE.md"]:
        s, d = os.path.join(SRC, p), os.path.join(REPO, p)
        if os.path.isdir(s):
            shutil.rmtree(d, ignore_errors=True)
            shutil.copytree(s, d)
        elif os.path.exists(s):
            shutil.copy2(s, d)
    os.makedirs(f"{REPO}/internal/foo", exist_ok=True)
    os.makedirs(f"{REPO}/acceptance/foo", exist_ok=True)
    open(f"{REPO}/internal/foo/foo_test.go", "w").write("package foo\n")
    open(f"{REPO}/acceptance/foo/foo_test.go", "w").write("package foo_test\n")
    git("add", "-A")
    git("commit", "-qm", "selftest fixtures", "--no-verify")
    git("branch", "-f", "main", "HEAD")
    git("branch", "-f", "staging", "HEAD")
    git("config", "core.hooksPath", ".githooks")
    git("switch", "-qc", "feat/F-001-demo")


def hook(name, payload):
    payload.setdefault("cwd", REPO)
    return subprocess.run([os.path.join(REPO, ".claude/hooks", name)], input=json.dumps(payload),
                          capture_output=True, text=True)


def report(name, ok, detail=""):
    global fails
    fails += not ok
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + ("" if ok else f"\n      {detail.strip()[:300]}"))


def case(name, tool, inp, expect, agent=""):
    d = {"tool_name": tool, "tool_input": inp, "hook_event_name": "PreToolUse"}
    if agent:
        d["agent_type"] = agent
    r = hook("guard.py", d)
    got = "block" if r.returncode == 2 else ("allow" if r.returncode == 0 else f"error {r.returncode}")
    report(f"{name:56s} {expect}", got == expect, f"got {got}: {r.stderr}")


def A(p):
    return os.path.join(REPO, p)


def guard_tests():
    print("-- builder: hidden tests and sensitive data")
    case("Read hidden acceptance test", "Read", {"file_path": A("acceptance/foo/foo_test.go")}, "block")
    case("Read normal source file", "Read", {"file_path": A("internal/foo/foo_test.go")}, "allow")
    case("Read real WhatsApp session", "Read", {"file_path": A("auth_info_baileys/creds.json")}, "block")
    case("Grep with no path (repo-wide)", "Grep", {"pattern": "func"}, "block")
    case("Grep in internal/", "Grep", {"pattern": "func", "path": A("internal")}, "allow")
    case("cat an acceptance test", "Bash", {"command": "cat acceptance/foo/foo_test.go"}, "block")
    case("sh -c head acceptance test", "Bash", {"command": "sh -c 'head acceptance/foo/foo_test.go'"}, "block")
    case("go test ./acceptance/... (results only)", "Bash", {"command": "go test ./acceptance/..."}, "allow")
    case("make check-fast", "Bash", {"command": "make check-fast"}, "allow")
    case("git show (can reveal tests)", "Bash", {"command": "git show HEAD"}, "block")
    case("git diff main (can reveal tests)", "Bash", {"command": "git diff main"}, "block")
    case("git diff main -- internal/", "Bash", {"command": "git diff main -- internal/"}, "allow")
    case("git diff --cached", "Bash", {"command": "git diff --cached"}, "allow")
    case("git log --oneline", "Bash", {"command": "git log --oneline -5"}, "allow")
    case("git diff --cached > /tmp/out.patch", "Bash", {"command": "git diff --cached > /tmp/out.patch"}, "allow")
    case("go test ./... 2>&1 | tail", "Bash", {"command": "go test ./... 2>&1 | tail -20"}, "allow")
    print("-- builder: searches (repo-wide blocked, targeted allowed)")
    case("grep -rn from repo root", "Bash", {"command": "grep -rn Revoke ."}, "block")
    case("grep -r with no path (cwd = root)", "Bash", {"command": "grep -r Revoke"}, "block")
    case("rg with no path (cwd = root)", "Bash", {"command": "rg Revoke"}, "block")
    case("find from repo root", "Bash", {"command": "find . -name '*.go'"}, "block")
    case("grep -rn in internal/", "Bash", {"command": "grep -rn Revoke internal/"}, "allow")
    case("grep in one file", "Bash", {"command": "grep -n package internal/foo/foo_test.go"}, "allow")
    case("grep on piped output", "Bash", {"command": "go list ./... | grep foo"}, "allow")
    case("rg on piped output", "Bash", {"command": "git log --oneline | rg feat"}, "allow")
    case("rg in internal/", "Bash", {"command": "rg -n Revoke internal"}, "allow")
    case("find in internal/", "Bash", {"command": "find internal -name '*.go'"}, "allow")
    print("-- builder: protected files")
    case("Write Makefile", "Write", {"file_path": A("Makefile")}, "block")
    case("Edit .claude/settings.json", "Edit", {"file_path": A(".claude/settings.json")}, "block")
    case("Edit CLAUDE.md", "Edit", {"file_path": A("CLAUDE.md")}, "block")
    case("Edit committed test", "Edit", {"file_path": A("internal/foo/foo_test.go")}, "block")
    case("Write new test file", "Write", {"file_path": A("internal/foo/new_test.go")}, "allow")
    open(A("internal/foo/staged_test.go"), "w").write("package foo\n")
    git("add", "internal/foo/staged_test.go")
    case("Edit own staged, never-committed test", "Edit", {"file_path": A("internal/foo/staged_test.go")}, "allow")
    git("commit", "-qm", "own test on the feature branch", "--no-verify")
    case("Edit own test committed only on this branch", "Edit", {"file_path": A("internal/foo/staged_test.go")}, "allow")
    case("git restore --staged own test", "Bash", {"command": "git restore --staged internal/foo/staged_test.go"}, "allow")
    case("Write production code", "Write", {"file_path": A("internal/foo/foo.go")}, "allow")
    case("Write a stamp directly", "Write", {"file_path": A(".agent/stamps/abc")}, "block")
    case("Edit features.json (passes/evidence)", "Edit", {"file_path": A("harness/features.json")}, "allow")
    case("sed -i on Makefile", "Bash", {"command": "sed -i '' s/a/b/ Makefile"}, "block")
    case("redirect into a hook", "Bash", {"command": "echo x > .claude/hooks/guard.py"}, "block")
    case("redirect via a variable", "Bash", {"command": "D=.claude/hooks; echo x > $D/guard.py"}, "block")
    case("cp onto a protected file", "Bash", {"command": "cp /tmp/x Makefile"}, "block")
    case("cp from a protected file (read only)", "Bash", {"command": "cp Makefile /tmp/Makefile.bak"}, "allow")
    case("cp from acceptance/ (hidden)", "Bash", {"command": "cp acceptance/foo/foo_test.go /tmp/x"}, "block")
    case("mv a protected file away", "Bash", {"command": "mv Makefile /tmp/"}, "block")
    case("git checkout -- committed test", "Bash", {"command": "git checkout -- internal/foo/foo_test.go"}, "block")
    case("git reset --hard", "Bash", {"command": "git reset --hard HEAD~1"}, "block")
    print("-- builder: git hooks setting")
    case("read core.hooksPath", "Bash", {"command": "git config core.hooksPath"}, "allow")
    case("read with --get", "Bash", {"command": "git config --get core.hooksPath"}, "allow")
    case("set core.hooksPath", "Bash", {"command": "git config core.hooksPath /dev/null"}, "block")
    case("unset core.hooksPath", "Bash", {"command": "git config --unset core.hooksPath"}, "block")
    print("-- builder: git workflow")
    case("git push origin main", "Bash", {"command": "git push origin main"}, "block")
    case("git push --force", "Bash", {"command": "git push --force origin feat/F-001-demo"}, "block")
    case("git push -u origin HEAD (on feat branch)", "Bash", {"command": "git push -u origin HEAD"}, "allow")
    case("gh pr merge", "Bash", {"command": "gh pr merge 12 --squash"}, "block")
    case("gh pr create --base staging", "Bash", {"command": "gh pr create --base staging --fill"}, "allow")
    case("gh pr create --base main (releases are human)", "Bash", {"command": "gh pr create --base main --fill"}, "block")
    case("gh pr create without --base (defaults to main)", "Bash", {"command": "gh pr create --fill"}, "block")
    case("git push origin staging", "Bash", {"command": "git push origin staging"}, "block")
    open(A("internal/foo/foo.go"), "w").write("package foo\n")
    git("add", "-A")
    case("git commit without judge stamp", "Bash", {"command": "git commit -m 'feat(F-001): x'"}, "block")
    case("git -C . commit without stamp", "Bash", {"command": "git -C . commit -m x"}, "block")
    case("git commit --no-verify", "Bash", {"command": "git commit --no-verify -m x"}, "block")
    case("git commit --amend", "Bash", {"command": "git commit --amend -m x"}, "block")
    print("-- other agents")
    case("architect writes acceptance test", "Write", {"file_path": A("acceptance/foo/new_test.go")}, "allow", "architect")
    case("architect writes a stub", "Write", {"file_path": A("internal/foo/api_stub.go")}, "allow", "architect")
    case("architect writes production code", "Write", {"file_path": A("internal/foo/foo.go")}, "block", "architect")
    case("judge reads acceptance test", "Read", {"file_path": A("acceptance/foo/foo_test.go")}, "allow", "judge")
    case("judge writes production code", "Write", {"file_path": A("internal/foo/foo.go")}, "block", "judge")
    case("judge writes scratch test", "Write", {"file_path": A(".agent/scratch/F-001/x_test.go")}, "allow", "judge")
    case("judge runs git add", "Bash", {"command": "git add -A"}, "block", "judge")
    case("explorer reads acceptance test", "Read", {"file_path": A("acceptance/foo/foo_test.go")}, "block", "explorer")
    case("retro writes an incident", "Write", {"file_path": A("harness/incidents/F-001-gate-loop.md")}, "allow", "retro")
    case("retro writes a dead end", "Write", {"file_path": A("harness/dead-ends/F-001.md")}, "allow", "retro")
    case("builder writes a plan-change record", "Write", {"file_path": A("harness/plan-changes/x.md")}, "block")
    case("builder writes its own scorecard file", "Write", {"file_path": A("harness/metrics/F-001.json")}, "block")
    case("retro edits a rules file", "Edit", {"file_path": A(".claude/rules/go.md")}, "block", "retro")


def flow_tests():
    stamps = os.path.join(REPO, ".agent/stamps")

    def guard_commit():
        return hook("guard.py", {"tool_name": "Bash", "tool_input": {"command": "git commit -m x"}}).returncode

    print("-- judge stamp -> commit (code change from the guard tests is staged)")
    hook("stamp.py", {"last_assistant_message": "FAIL\nfoo.go:1 — blocker — ...", "agent_type": "judge"})
    report("judge FAIL writes no stamp", not os.path.isdir(stamps) or not os.listdir(stamps))
    report("commit still blocked after FAIL", guard_commit() == 2)
    r = hook("stamp.py", {"last_assistant_message": "PASS\nno findings", "agent_type": "judge"})
    report("judge PASS + green gate writes a stamp", os.path.isdir(stamps) and len(os.listdir(stamps)) == 1, r.stderr)
    report("Claude-side commit gate allows the commit", guard_commit() == 0)
    r = git("commit", "-qm", "feat(F-001): demo")
    report("git pre-commit accepts the stamped diff", r.returncode == 0, r.stderr)
    open(A("internal/foo/foo.go"), "a").write("// changed after review\n")
    git("add", "-A")
    report("change after review: Claude-side gate blocks", guard_commit() == 2)
    r = git("commit", "-qm", "sneaky")
    report("change after review: git pre-commit blocks", r.returncode != 0, r.stdout + r.stderr)
    git("reset", "-q", "--hard", "HEAD")
    os.makedirs(A("harness/progress"), exist_ok=True)
    open(A("harness/progress/F-001.md"), "w").write("- note\n")
    git("add", "-A")
    r = git("commit", "-qm", "chore: progress")
    report("bookkeeping-only commit needs no stamp", r.returncode == 0, r.stderr)

    print("-- judge verdict delivered through the transcript hand-back")

    def transcript(verdict):
        path = os.path.join(TMP, f"judge-{verdict}.jsonl")
        entry = {"type": "assistant", "message": {"role": "assistant", "content": [
            {"type": "tool_use", "name": "SubagentHandback", "input": {"message": f"{verdict}\nreport body"}}]}}
        with open(path, "w") as f:
            f.write(json.dumps({"type": "user", "message": {"role": "user", "content": "review F-001"}}) + "\n")
            f.write(json.dumps(entry) + "\n")
        return path
    open(A("internal/foo/foo.go"), "a").write("// second change\n")
    git("add", "-A")
    hook("stamp.py", {"last_assistant_message": "", "agent_type": "judge", "agent_transcript_path": transcript("FAIL")})
    report("hand-back FAIL writes no stamp", not os.listdir(stamps))
    r = hook("stamp.py", {"last_assistant_message": "", "agent_type": "judge", "agent_transcript_path": transcript("PASS")})
    report("hand-back PASS (empty last message) writes a stamp", len(os.listdir(stamps)) == 1, r.stderr)
    report("commit allowed after hand-back PASS", guard_commit() == 0)
    git("reset", "-q", "--hard", "HEAD")

    print("-- tamper audit")

    def tamper():
        return subprocess.run(["python3", "harness/scripts/tamper.py"], cwd=REPO, capture_output=True, text=True)
    r = tamper()
    report("clean branch passes", r.returncode == 0, r.stdout)
    open(A("internal/foo/foo_test.go"), "w").write("")
    report("removing lines from a committed test fails", tamper().returncode == 1)
    git("checkout", "--", "internal/foo/foo_test.go")
    open(A("internal/foo/foo.go"), "a").write("var x = 1 //nolint\n")
    report("adding //nolint fails", tamper().returncode == 1)
    git("checkout", "--", "internal/foo/foo.go")
    fj = A("harness/features.json")
    base = {"version": 1, "features": [{"id": "F-001", "title": "a", "passes": False, "evidence": []}]}
    json.dump(base, open(fj, "w"))
    git("add", "-A")
    git("commit", "-qm", "seed", "--no-verify")
    git("branch", "-f", "main", "HEAD")
    git("branch", "-f", "staging", "HEAD")
    base["features"][0].update(passes=True, evidence=["TestX"])
    json.dump(base, open(fj, "w"))
    report("features.json: flipping passes/evidence is fine", tamper().returncode == 0)
    base["features"][0]["title"] = "changed"
    json.dump(base, open(fj, "w"))
    report("features.json: changing anything else fails", tamper().returncode == 1)
    os.makedirs(A("harness/plan-changes"), exist_ok=True)
    open(A("harness/plan-changes/2026-01-01-rescope.md"), "w").write("# rescope F-001\n")
    report("features.json: plan change with a human record passes", tamper().returncode == 0)
    os.remove(A("harness/plan-changes/2026-01-01-rescope.md"))
    git("checkout", "--", "harness/features.json")

    print("-- Stop gate")
    plan = A(".agent/plan.md")
    open(plan, "w").write("# F-001\n- [x] 1. Orient\n- [ ] 2. Pick\n")
    codes = [hook("stop_gate.py", {"session_id": "s1"}).returncode for _ in range(5)]
    report("open checklist continues the turn, capped at 3", codes == [2, 2, 2, 0, 0], str(codes))
    open(plan, "w").write("BLOCKED: waiting for a test-change request\n- [ ] 2. Pick\n")
    report("BLOCKED lets the session stop", hook("stop_gate.py", {"session_id": "s2"}).returncode == 0)
    os.remove(plan)
    report("no active feature: stop is allowed", hook("stop_gate.py", {"session_id": "s3"}).returncode == 0)

    print("-- session summary, scorecard, report")
    r = hook("session_start.py", {"source": "startup"})
    report("session start prints a state summary", "Harness state" in r.stdout, r.stdout + r.stderr)
    r = subprocess.run(["python3", "harness/scripts/scorecard.py", "F-001"], cwd=REPO, capture_output=True, text=True)
    report("scorecard writes a line", r.returncode == 0 and os.path.exists(A("harness/metrics/F-001.json")), r.stderr)
    r = subprocess.run(["python3", "harness/scripts/report.py"], cwd=REPO, capture_output=True, text=True)
    report("report runs", r.returncode == 0 and "features scored" in r.stdout, r.stdout + r.stderr)


if __name__ == "__main__":
    try:
        setup()
        guard_tests()
        flow_tests()
    finally:
        shutil.rmtree(TMP, ignore_errors=True)
    print(f"\n{'all passed' if not fails else f'{fails} FAILED'}")
    sys.exit(1 if fails else 0)

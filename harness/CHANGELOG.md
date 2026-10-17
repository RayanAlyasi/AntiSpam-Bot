# Harness changelog

Every change to CLAUDE.md, rules, skills, agents, hooks or checks: date, what changed (one change per entry), evidence, `/harness-eval` before/after results, and the harness version.

- 2026-10-16 — Initial harness (docs/AGENTIC_ENVIRONMENT.md revision 3).
- 2026-10-17 — Guard fixes from first real use, plus a self-test (`make selftest`, 85 cases, all passing):
  - `git config core.hooksPath` is now blocked only when it *changes* the setting;
  - `grep`/`rg`/`find` are blocked only for repo-wide searches;
  - `cp`/`ln`/`rsync` sources are treated as reads, not writes;
  - redirections (`> file`, `2>&1`) are no longer read as command arguments;
  - paths are compared after resolving symlinks. Before this, a path given through a symlink such as macOS `/var` → `/private/var` slipped past every protection.

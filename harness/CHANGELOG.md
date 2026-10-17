# Harness changelog

Every change to CLAUDE.md, rules, skills, agents, hooks or checks: date, what changed (one change per entry), evidence, `/harness-eval` before/after results, and the harness version.

- 2026-10-16 — Initial harness (docs/AGENTIC_ENVIRONMENT.md revision 3).
- 2026-10-17 — Guard fixes from first real use, plus a self-test (`make selftest`, 85 cases, all passing):
  - `git config core.hooksPath` is now blocked only when it *changes* the setting;
  - `grep`/`rg`/`find` are blocked only for repo-wide searches;
  - `cp`/`ln`/`rsync` sources are treated as reads, not writes;
  - redirections (`> file`, `2>&1`) are no longer read as command arguments;
  - paths are compared after resolving symlinks. Before this, a path given through a symlink such as macOS `/var` → `/private/var` slipped past every protection.
- 2026-10-17 — Branch workflow and history style:
  - feature PRs target `staging`, and releases go `staging` → `main` by PR (GitHub ruleset, no bypass);
  - the guard blocks pushes to `staging` and any `gh pr create` without `--base staging`;
  - tamper and lint baselines use the base branch;
  - no AI attribution in commits or PRs;
  - engineer-style commit and PR rules, plus a PR template.
- 2026-10-17 — The judge stamp hook now reads the verdict from the SubagentHandback call in the judge transcript when last_assistant_message is empty, which it is when a subagent reports through a hand-back. Found when F-000 got two PASS reviews but no stamp. Self-test: 91 cases.
- 2026-10-17 — Commits and PR titles switch to Conventional Commits, so release-please can generate the changelog and pick versions. Feature PRs are squash-merged with the PR title. User-facing docs are updated in the same PR, and the judge treats a missing docs update as a major finding.

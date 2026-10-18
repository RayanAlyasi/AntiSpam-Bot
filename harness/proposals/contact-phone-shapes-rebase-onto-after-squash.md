# Proposal: stacked branches rebase with --onto after squash merge

- Rule: If a branch is based on another feature branch that was squash-merged, rebase with `git rebase --onto origin/TARGET PARENT_TIP_SHA` and note the parent tip in progress, because a plain rebase replays the parent's pre-squash commits.
- File: docs/AGENTIC_ENVIRONMENT.md (git/branching section) or the next-feature skill rules.
- Incident: harness/incidents/contact-phone-shapes-rebase-squash-merged-base.md

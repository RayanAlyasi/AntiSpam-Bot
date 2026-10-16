---
name: harness-curate
description: Runs the curator to turn incidents and proposals into a reviewed playbook diff. Human-started only; intended from about 20–30 finished features onward, or after an alarm.
disable-model-invocation: true
---
1. Count finished features (`passes: true` in `harness/features.json`). If fewer than 20, tell the user curation is premature (there isn't enough evidence to separate real lessons from noise) and ask whether to continue anyway.
2. Delegate to the **curator** agent.
3. Show the user `rationale.md` and the patches it produced.
4. Apply nothing. If the user approves specific operations, they apply them by hand (agents can't edit `.claude/`), then run `/harness-eval` for any change to CLAUDE.md, skills, agents or hooks.

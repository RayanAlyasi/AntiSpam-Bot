# WP-G: early F-102 PR from a second worktree abandoned

- Symptom: an F-102 PR from another worktree was abandoned and F-102 folded into the WP-G bundle.
- Root cause: the judge's stamp is bound to the session's working directory, so work reviewed in one worktree cannot be committed from another.
- Evidence: caller report (no log file); F-102 is in the WP-G bundle in harness/progress/F-041.md.
- Would have prevented: one worktree per feature or bundle decided at plan time; documenting the stamp binding in docs/AGENTIC_ENVIRONMENT.md.

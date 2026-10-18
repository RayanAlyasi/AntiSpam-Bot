# Proposal: sweep user docs and DESIGN when changing user-visible behavior

Rule: When a change alters user-visible behavior, grep docs/ (user docs and DESIGN tables such as the section 18 mode rows) for the old behavior and update them in the same commit, because the judge blocked on stale docs/action-queue.md and the silent-mode row.

Belongs in: the builder checklist in .claude/ rules for /next-feature (or CLAUDE.md "Working style").

From incident: harness/incidents/shadow-reactions-rails-demoted-shadow.md.

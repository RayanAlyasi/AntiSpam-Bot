# Proposal: gate behavior on the effective plan, and on route rather than verdict

Rule: When gating behavior on detection mode or classification, read the effective plan after the safety rails (Rails.Admit) and key on the route (act/ask/ignore), not the configured mode or the raw verdict, because rails can demote enforce groups to shadow and an uncertain message has verdict clean but route ask.

Belongs in: the policy/lane rules file (for example .claude/rules for internal/policy or the architecture notes in docs/AGENTIC_ENVIRONMENT.md or DESIGN section on the lane).

From incident: harness/incidents/shadow-reactions-rails-demoted-shadow.md (and dead-ends/shadow-reactions.md).

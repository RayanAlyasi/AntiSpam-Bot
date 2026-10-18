# Proposal: gate behavior on the effective plan, and on route rather than verdict

Rule: Behaviour that belongs to a group's configured mode (such as shadow-only output) must check the configured `Settings.Mode` as well as the plan after the safety rails. `Rails.Admit` can demote an enforce group's plan to detection mode shadow (circuit breaker, global switch), so the plan's mode alone isn't the group's mode. Key the choice on the route (act/ask/ignore), not the raw verdict: a still-uncertain message has verdict clean but route ask.

Belongs in: the policy/lane rules file (for example .claude/rules for internal/policy or the architecture notes in docs/AGENTIC_ENVIRONMENT.md or DESIGN section on the lane).

From incident: harness/incidents/shadow-reactions-rails-demoted-shadow.md (and dead-ends/shadow-reactions.md).

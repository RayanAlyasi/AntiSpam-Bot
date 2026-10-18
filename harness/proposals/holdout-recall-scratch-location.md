# Proposal: keep scratch probes out of linted and guarded directories

Rule: put throwaway probe tests only in .agent/scratch/ and delete them before hand-off, because anything under the hidden tests dir is linted but the builder can't edit it, which leaves the stop gate red.

Belongs in: docs/AGENTIC_ENVIRONMENT.md (architect section) and the architect agent prompt.

Source incident: harness/incidents/holdout-recall-scratch-probe-blocked-gate.md

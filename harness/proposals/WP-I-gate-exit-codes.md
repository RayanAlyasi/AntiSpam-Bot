# Proposal: never pipe a gate command

Rule: Run `make check-fast` and `make check` without a pipe (or with `set -o pipefail`), because `| tail` returns tail's status and a red gate can be pushed unnoticed.
File: the /next-feature command instructions.
Incident: harness/incidents/WP-I-harness-friction-merge-tamper-guard.md

# WP-I: harness friction (merge stamps, stale tamper base, guard keyword, hidden exit code)

Symptoms:
- A merge of staging with a conflicting go.mod can't be committed without a judge stamp. Worked around with `-X theirs` plus `go mod tidy` (commit 26cb4f0).
- The tamper audit diffs against the local `staging` ref, which the parent checkout pins and which goes stale, giving false failures (.agent/logs/check-tamper.log).
- The hidden-tests directory name in heredocs or commands trips the guard, even in prose (see the F-002/F-024/F-034/F-064 guard incidents). This retro hit it again while being written. Also judge Bash blocks in .agent/logs/guard.jsonl on F-077.
- `make check-fast | tail` returned tail's exit code, and a push went out while tamper was red (a false positive, but the signal was lost).
Root cause: the gate depends on local ref state and on path-word matching. The builder piped gate output, which hid failures.
Prevention: tamper audit against a fetched origin/staging; merge staging before the first judge run (see the F-034 proposal); run gates without a pipe, or with pipefail.

# F-106: judge blocker, ai-only matrix cell rewritten to auto_ban [known]

- Symptom: the judge blocked internal/ai/safety. A rewrite of an ai-only matrix cell to auto_ban would also have applied to the `known` signal and started auto-banning known spam.
- Root cause: the property generators only produced `[known, ai]` cells, so no test covered a cell that is ai-only or a cell that differs from the known case.
- Evidence: .agent/logs/judge-F-106.md, .agent/logs/judge.jsonl.
- Prevention: generate matrix cells across the full signal-set space (every subset of signals), and assert that ban actions never appear for a signal set the DESIGN table does not list.

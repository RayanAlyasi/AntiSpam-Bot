# F-002: guard false positives on Bash commands (3 variants)

- Symptom 1: any Bash command whose text contains the word "acceptance" was blocked, including heredoc bodies (progress notes, commit text). Several retries; files had to be written with the Write tool.
- Symptom 2: a new, staged-but-uncommitted test file (`cmd/antispam/roles_test.go`) was treated as a "committed test"; appending to it was blocked, though the builder had created it.
- Symptom 3: `ls .agent/stamps/; git commit ...` was refused with "No judge stamp for this exact staged diff" although the stamp existed and its hash matched. The same commit run alone succeeded.
- Root cause: the guard does text matching on the whole command string, and its "committed" test appears to use the git index rather than HEAD. The commit check likely hashes or parses the command in a way that breaks when chained.
- Evidence: `.agent/logs/guard.jsonl` (20 blocks in total); `.agent/logs/judge.jsonl`.
- Would have prevented: guard matching on target paths rather than any mention of a word; "committed" meaning present in HEAD; the stamp check handling chained commands, or the docs saying to run `git commit` alone.

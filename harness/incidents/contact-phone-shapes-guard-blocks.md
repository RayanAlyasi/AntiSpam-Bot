# contact-phone-shapes: guard blocks from mentioning the hidden test dir

- Symptom: about 6 guard blocks for the main agent (17:21-17:36), plus architect and judge write-scope blocks and one "no judge stamp" block.
- Root cause: heredocs and greps that merely contained the hidden test directory name, grep -r over docs/ (whole-repo grep), and `git diff <rev>` without a pathspec. Judge blocks: writing a scratch test into internal/ and a stray `=3,` redirect target.
- Evidence: .agent/logs/guard.jsonl (branch feat/contact-phone-shapes).
- Prevention: use the Grep tool with a path, or grep with an explicit directory. Avoid the hidden directory name in commands. Use `git diff main -- internal/`. Judge scratch tests go under .agent/scratch with -overlay. Same family as the F-002, F-024 and F-034 guard incidents.

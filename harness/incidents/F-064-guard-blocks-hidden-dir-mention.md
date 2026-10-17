# F-064 bundle: guard blocked commands and PR bodies that only mention the hidden test directory

Symptom: guard.jsonl shows main-agent blocks (16:41:11Z, 17:20:22Z) with "the directory is hidden from you" for commands that only named it (PR body / bookkeeping), not read it. Also a judge block (16:31:12Z, 16:42:40Z) for `>` redirects including `/dev/null` ("may only write .agent/scratch/, .agent/logs/judge-*"), and a main block (16:56:32Z) for an edit attempt on the committed regress_test.go.

Root cause: the guard matches the name or a redirect token anywhere in the command string, instead of path arguments of commands that read or write. Same cause as F-034-guard-blocks-hidden-dir-word.md (recurrence).

Evidence: .agent/logs/guard.jsonl entries above.

Prevention: guard should only inspect path operands of read/write commands and allow `/dev/null`; until then tell builders to keep the directory name out of PR bodies and commit messages and use the Write tool for text. The regress_test.go block was correct (committed test); new tests belong in new files.

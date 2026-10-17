# F-034 bundle: guard blocks any Bash command containing the hidden-tests directory name

Symptom: guard.jsonl shows four main-agent blocks ("the hidden-tests directory is hidden from you") at 13:22, 13:45, 16:21 and 16:25, plus blocks on git commands that could reveal tests (13:20, 13:27). Triggers included a commit-message heredoc and a git pathspec exclude, neither of which reads the directory. (This reviewer's own Bash write of this very note was blocked the same way.)

Root cause: the guard matches the word anywhere in the command string rather than a path being read.

Evidence: .agent/logs/guard.jsonl. Bookkeeping had to go through the Write tool.

Second block: at 16:44 the guard refused the staging merge commit because no judge stamp covered it. Merging staging after a PASS invalidates the stamp and cost a fourth judge round (merge-only review).

Prevention: tell the builder in /next-feature to merge staging before the first judge run and to keep the word out of commit bodies and pathspecs; or have the guard match only path arguments of read commands (needs human approval).

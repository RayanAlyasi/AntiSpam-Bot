# Test-change approvals

Existing tests (from earlier, reviewed work) can't be changed by agents, and the tamper audit rejects edits to them. When a change is genuinely right (a brittle test, a wrong expectation), a human approves it here: one file per approval, `<date>-<slug>.md`, with a `branch: <branch name>` line, one `- <path>` line per test file, and the reason. Only that branch may change those files. Agents can't write here.

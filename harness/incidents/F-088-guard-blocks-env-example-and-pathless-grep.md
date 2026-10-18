# F-088 bundle: guard blocked `.env.example`, hidden-dir word in commands, and pathless greps

- Symptom: 25 guard blocks. Creating `.env.example` was refused, so the template ships as `env.example`. Commands that merely contained the hidden-test directory's name were refused, as were greps with no path operand (they would recurse into it). The judge was blocked writing outside `.agent/scratch/` and `.agent/logs/judge-*` (for example a test log under the session scratchpad, and a stray test file name in a command).
- Root cause: the guard matches names anywhere in the command string and treats dotenv-style files as secrets. Recurrence of F-064-guard-blocks-hidden-dir-mention.md and F-034-guard-blocks-hidden-dir-word.md.
- Evidence: `.agent/logs/guard.jsonl` (branch feat/F-088-fast-lane); `harness/proposed/guard-env-example.md`.
- Would have prevented: giving every grep an explicit path (`internal/ cmd/`), keeping the hidden directory's name out of commands, PR bodies and commit text, and an allowlist for `*.example` templates in the guard. Judge output should go to `.agent/scratch/`.

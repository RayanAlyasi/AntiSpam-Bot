# F-024: guard false positives

- **Symptom:** 13 guard blocks, most on harmless commands: grep over `internal/ cmd/` plus `docs/DESIGN.md`, `git diff --stat go.mod go.sum` (read as a diff against another revision), and any shell command whose text merely contained the hidden test directory's path (an evidence string for features.json, a PR body heredoc, retro notes). The retro agent's own notes were lost to the same block.
- **Root cause:** guard.py pattern-matches the whole command text, not the paths a command touches.
- **Evidence:** harness/metrics/F-024.json (guard_blocks: 13); this session's transcript.
- **Prevention:** have the guard match only path arguments and git revision ranges. Until then, write prose files (PR bodies, notes) with the Write tool, not heredocs.

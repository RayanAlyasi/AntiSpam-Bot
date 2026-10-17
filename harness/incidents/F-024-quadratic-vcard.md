# F-024: quadratic vCard unfolding

- **Symptom:** judge round 1 major: a 64 KiB folded card took 62 ms, 50 cards in one contacts array 3.4 s.
- **Root cause:** each continuation line appended with `+=` and re-upper-cased the whole line; the 64 KiB per-card cap didn't bound the number of cards per message.
- **Evidence:** .agent/logs/judge-F-024.md, .agent/scratch/F-024/break_test.go (TestVCardFoldingCost); fixed in 8c37be6 (linear unfold, 32-card cap).
- **Prevention:** parsers of attacker input need a cost test on a worst-case input (long folds, many items), not only a size cap and a fuzz test; fuzzing doesn't flag slow-but-correct inputs.

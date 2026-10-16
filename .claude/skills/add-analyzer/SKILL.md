---
name: add-analyzer
description: Adds a detection analyzer for a content type or signal in the tiered pipeline. Use when a feature adds or changes spam detection for any message kind.
---
Read `docs/DESIGN.md` §6 (analyzers), §7.0 (certainty routing) and §7.3 (scoring) first.

1. Implement the `Analyzer` interface from DESIGN.md §6 in `internal/detect/<name>/`. Declare its tier honestly: T0 does no I/O, and T1 uses only cached I/O.
2. Emit `Signal`s with an evidence string a human admin can understand. A new heuristic is a weighted signal, never a hard rule, until review data supports more.
3. Tests:
   - table tests using real envelopes from `testdata/fixtures/`;
   - at least one "normal message that looks spammy" case that must *not* fire;
   - a fuzz test if it parses text;
   - a property test if it must hold an invariant.
4. Register it in the pipeline, with its policy toggle if DESIGN.md §12 defines one.

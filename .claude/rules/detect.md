---
paths:
  - "internal/detect/**"
  - "internal/normalize/**"
  - "internal/scoring/**"
  - "internal/policy/**"
---
# Detection and scoring

- [DET-001] Certainty routing (DESIGN.md §7.0): certain → act per the auto-ban matrix; uncertain → AI; still uncertain → admins.
- [DET-002] A false positive costs more than a missed spam. A new heuristic starts as a weighted signal, never a hard rule.
- [DET-003] Identity priors (number prefix, picture or name reuse) never delete, mute or ban on their own, and never count as a signal family.
- [DET-004] Normalization keeps the raw text for evidence and produces a normalized form for matching. Each step is a separate, tested function.
- [DET-005] Normal-message text is never stored long-term or used for learning (DESIGN.md §7.5, §17).

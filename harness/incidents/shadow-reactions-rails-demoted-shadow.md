# shadow-reactions: reactions enqueued for enforce groups demoted to shadow

Symptom: judge FAILed round 1. shadowReaction checked only Plan.Mode == DetectionShadow, so an enforce group demoted to shadow by the safety rails (deletion circuit) still enqueued reactions. The judge also flagged a stale user doc (docs/action-queue.md) and the DESIGN section 18 silent-mode row. Round 2 PASSed.

Root cause: the builder did not know policy.Rails.Admit can set the shadow detection mode inside an enforce group, so "shadow" was read from configured mode, not the effective plan after rails. Docs were not grepped for the behavior being changed.

Evidence: .agent/logs/ judge report round 1; commit ccb5264 (fixed version); PR #66.

Prevention: read the effective post-rails plan when gating on mode, and list every writer of the mode field (grep for DetectionShadow) before coding. Grep docs/ and DESIGN for the old behavior when changing user-visible behavior.
